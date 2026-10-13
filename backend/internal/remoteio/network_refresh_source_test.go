package remoteio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type networkRefreshPeer struct {
	target, proxy           *httptest.Server
	connections, proxyCalls atomic.Int32
	proxyPosts              atomic.Int32
	mu                      sync.Mutex
	headers                 []string
	current                 *websocket.Conn
}

func newNetworkRefreshPeer(t *testing.T, ctx context.Context, hybrid bool) *networkRefreshPeer {
	p := &networkRefreshPeer{}
	handle := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var payload struct {
				Events []json.RawMessage `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			p.mu.Lock()
			conn := p.current
			p.mu.Unlock()
			if conn == nil {
				t.Error("POST before owned WebSocket connected")
				w.WriteHeader(409)
				return
			}
			for _, event := range payload.Events {
				if strings.Contains(string(event), "network-close-trigger") {
					conn.Close(websocket.StatusGoingAway, "private reconnect")
				} else if err := conn.Write(ctx, websocket.MessageText, []byte("network-update-ready\n")); err != nil {
					t.Error(err)
				}
			}
			w.WriteHeader(200)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		index := p.connections.Add(1)
		p.mu.Lock()
		p.current = conn
		p.headers = append(p.headers, r.Header.Get("Authorization")+"|"+r.Header.Get("x-environment-runner-version"))
		p.mu.Unlock()
		if hybrid && index == 2 {
			conn.Write(ctx, websocket.MessageText, []byte("network-refreshed-physical\n"))
			conn.Close(websocket.StatusCode(4001), "private retired")
			return
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if index == 1 && !strings.Contains(string(data), "network-close-trigger") {
				if err = conn.Write(ctx, websocket.MessageText, []byte("network-update-ready\n")); err != nil {
					t.Error(err)
					return
				}
			}
			if strings.Contains(string(data), "network-close-trigger") {
				if index == 1 {
					conn.Close(websocket.StatusGoingAway, "private reconnect")
				} else {
					conn.Write(ctx, websocket.MessageText, []byte("network-refreshed-physical\n"))
					conn.Close(websocket.StatusCode(4001), "private retired")
				}
				return
			}
		}
	})
	p.target = httptest.NewServer(handle)
	endpoint, _ := url.Parse(p.target.URL)
	p.proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.proxyCalls.Add(1)
		if r.Method == http.MethodPost {
			p.proxyPosts.Add(1)
		}
		if r.Method != http.MethodConnect {
			if r.URL.Host != endpoint.Host {
				t.Error("unowned proxy destination", r.URL.Host)
				w.WriteHeader(403)
				return
			}
			handle(w, r)
			return
		}
		if r.Host != endpoint.Host {
			t.Error("unowned proxy tunnel", r.Host)
			w.WriteHeader(403)
			return
		}
		upstream, err := net.Dial("tcp", endpoint.Host)
		if err != nil {
			t.Error(err)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		defer upstream.Close()
		defer client.Close()
		fmt.Fprint(buffer, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		joined := make(chan struct{})
		go func() { io.Copy(client, upstream); client.Close(); close(joined) }()
		io.Copy(upstream, buffer)
		upstream.Close()
		<-joined
	}))
	t.Cleanup(func() { p.target.Close(); p.proxy.Close() })
	return p
}

func TestActualSourceEnvironmentUpdateRefreshesProxyAndAuthOnWebSocketReconnect(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		t.Run(fmt.Sprintf("hybrid-%v", hybrid), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			env := map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-network-first", "CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION": "first", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": ""}
			if hybrid {
				env["CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2"] = "1"
			}
			messages := []any{map[string]any{"type": "result", "uuid": "network-first", "result": "first"}, map[string]any{"type": "result", "uuid": "network-close-trigger", "result": "close"}}
			source := newNetworkRefreshPeer(t, ctx, hybrid)
			updates := map[string]string{"HTTP_PROXY": source.proxy.URL, "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-network-refreshed", "CLAUDE_CODE_ENVIRONMENT_RUNNER_VERSION": "second"}
			want := runSourceFixture(t, "native-remoteio-ws.fixture.ts", map[string]any{"url": strings.Replace(source.target.URL, "http", "ws", 1) + "/ws/network", "env": env, "messages": messages, "runtimeNetwork": true, "proxyUrl": source.proxy.URL, "updates": updates})
			peer := newNetworkRefreshPeer(t, ctx, hybrid)
			factory := func(environment map[string]string) (*http.Client, error) {
				transport := http.DefaultTransport.(*http.Transport).Clone()
				if environment["HTTP_PROXY"] != "" {
					proxy, err := url.Parse(environment["HTTP_PROXY"])
					if err != nil {
						return nil, err
					}
					transport.Proxy = http.ProxyURL(proxy)
				} else {
					transport.Proxy = nil
				}
				return &http.Client{Transport: transport}, nil
			}
			service, err := New(ctx, Options{URL: strings.Replace(peer.target.URL, "http", "ws", 1) + "/ws/network", Environment: env, WebSocketHTTP: factory, GlobalHTTP: factory})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			service.Start()
			if err = service.WriteMessage(object(raw(messages[0]))); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(service)
			first, err := reader.ReadString('\n')
			if err != nil || first != "network-update-ready\n" {
				t.Fatal(err, first)
			}
			updates["HTTP_PROXY"] = peer.proxy.URL
			service.UpdateEnvironment(updates)
			if err = service.WriteMessage(object(raw(messages[1]))); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			source.mu.Lock()
			sourceHeaders := append([]string(nil), source.headers...)
			source.mu.Unlock()
			peer.mu.Lock()
			goHeaders := append([]string(nil), peer.headers...)
			peer.mu.Unlock()
			if string(data) != want.String("incoming") || service.CloseCode() != 4001 || source.connections.Load() != 2 || peer.connections.Load() != 2 || source.proxyCalls.Load() < 1 || peer.proxyCalls.Load() < 1 || !reflect.DeepEqual(sourceHeaders, goHeaders) {
				t.Fatalf("Source incoming%s headers%v proxy%d Go incoming%s headers%v proxy%d close%d", want.Get("incoming"), sourceHeaders, source.proxyCalls.Load(), data, goHeaders, peer.proxyCalls.Load(), service.CloseCode())
			}
			if hybrid && (source.proxyPosts.Load() != 1 || peer.proxyPosts.Load() != source.proxyPosts.Load()) {
				t.Fatalf("Source runtime HTTP proxy POSTs %d Go %d", source.proxyPosts.Load(), peer.proxyPosts.Load())
			}
		})
	}
}
