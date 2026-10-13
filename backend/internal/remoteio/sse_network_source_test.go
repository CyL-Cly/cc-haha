package remoteio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sseNetworkPeer struct {
	target, proxy           *httptest.Server
	mu                      sync.Mutex
	headers                 []string
	requests, proxyRequests atomic.Int32
	release                 chan struct{}
	once                    sync.Once
}

func newSSENetworkPeer(t *testing.T) *sseNetworkPeer {
	p := &sseNetworkPeer{release: make(chan struct{})}
	var handle http.HandlerFunc
	handle = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release-network" {
			p.once.Do(func() { close(p.release) })
			w.WriteHeader(200)
			return
		}
		index := p.requests.Add(1)
		p.mu.Lock()
		p.headers = append(p.headers, r.Header.Get("Authorization")+"|"+r.Header.Get("x-environment-runner-version"))
		p.mu.Unlock()
		if index > 2 {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload := map[string]any{"type": "keep_alive"}
		if index == 1 {
			payload = map[string]any{"type": "update_environment_variables", "variables": map[string]string{"HTTP_PROXY": p.proxy.URL, "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-sse-second", "CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION": "second"}}
		}
		fmt.Fprintf(w, "event: client_event\nid: %d\ndata: %s\n\n", index, raw(map[string]any{"event_id": fmt.Sprint(index), "payload": payload}))
		w.(http.Flusher).Flush()
		if index == 1 {
			select {
			case <-p.release:
			case <-r.Context().Done():
			}
		}
	}
	p.target = httptest.NewServer(handle)
	endpoint, _ := url.Parse(p.target.URL)
	p.proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != endpoint.Host {
			t.Error("unowned SSE proxy destination", r.URL.Host)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path != "/release-network" {
			p.proxyRequests.Add(1)
		}
		handle(w, r)
	}))
	t.Cleanup(func() { p.once.Do(func() { close(p.release) }); p.target.Close(); p.proxy.Close() })
	return p
}

func TestActualSourceSSERuntimeNetworkUpdateReachesNextFetchAndAuth(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-sse-first", "CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION": "first", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": ""}
	source := newSSENetworkPeer(t)
	want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"url": source.target.URL + "/sessions/network", "env": env, "sseNetwork": true})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	peer := newSSENetworkPeer(t)
	service, err := New(ctx, Options{URL: peer.target.URL + "/sessions/network", Environment: env, GlobalHTTP: func(environment map[string]string) (*http.Client, error) {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		if environment["HTTP_PROXY"] != "" {
			proxy, err := url.Parse(environment["HTTP_PROXY"])
			if err != nil {
				return nil, err
			}
			transport.Proxy = http.ProxyURL(proxy)
		}
		return &http.Client{Transport: transport}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	// This exercises the exact SSE stream and StructuredIO environment consumer,
	// separately from CCR worker registration, as the original transport fixture.
	service.ccr = nil
	service.spawn(service.sseLoop)
	reader := bufio.NewReader(service)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	update := object([]byte(first))
	if update.String("type") != "update_environment_variables" {
		t.Fatal(first)
	}
	service.UpdateEnvironment(map[string]string{"HTTP_PROXY": peer.proxy.URL, "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-sse-second", "CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION": "second"})
	response, err := http.Get(peer.target.URL + "/release-network")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	sourceHeaders := append([]string(nil), source.headers...)
	source.mu.Unlock()
	peer.mu.Lock()
	goHeaders := append([]string(nil), peer.headers...)
	peer.mu.Unlock()
	var incoming []string
	if err := json.Unmarshal(want.Get("incoming"), &incoming); err != nil || len(incoming) != 2 {
		t.Fatal(err, string(want.Get("incoming")))
	}
	if !reflect.DeepEqual(sourceHeaders, goHeaders) || source.proxyRequests.Load() != peer.proxyRequests.Load() || source.proxyRequests.Load() != 2 || service.CloseCode() != 401 || string(rest) != incoming[1] {
		t.Fatalf("Source headers%v proxy%d incoming%s Go headers%v proxy%d incoming%s close%d", sourceHeaders, source.proxyRequests.Load(), want.Get("incoming"), goHeaders, peer.proxyRequests.Load(), rest, service.CloseCode())
	}
}
