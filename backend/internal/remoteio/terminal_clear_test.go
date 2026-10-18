package remoteio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceIngressClearKeepsOldAdmissionAndReadsCurrentGlobalAck(t *testing.T) {
	type privatePeer struct {
		server           *httptest.Server
		started, release chan struct{}
		once             sync.Once
		mu               sync.Mutex
		trace            []map[string]string
	}
	newPeer := func() *privatePeer {
		p := &privatePeer{started: make(chan struct{}), release: make(chan struct{})}
		p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/control/old-started" {
				select {
				case <-p.started:
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(200)
				return
			}
			if r.URL.Path == "/control/release-old" {
				p.once.Do(func() { close(p.release) })
				w.WriteHeader(200)
				return
			}
			body, _ := io.ReadAll(r.Body)
			entry := object(body)
			p.mu.Lock()
			p.trace = append(p.trace, map[string]string{"method": r.Method, "uuid": entry.String("uuid"), "last": r.Header.Get("Last-Uuid"), "auth": r.Header.Get("Authorization")})
			p.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if r.Method == "GET" {
				_, _ = io.WriteString(w, `{"loglines":[{"type":"user","uuid":"seed-head"}]}`)
				return
			}
			if entry.String("uuid") == "old-admitted" {
				close(p.started)
				select {
				case <-p.release:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{}`)
		}))
		t.Cleanup(func() { p.once.Do(func() { close(p.release) }); p.server.Close() })
		return p
	}
	env := map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-owned-ingress-clear"}
	old := newPeer()
	source := runSourceFixture(t, "native-terminal-ingress-clear.fixture.ts", map[string]any{"url": old.server.URL + "/ingress/actual", "env": env})
	if string(source.Get("results")) != "[true,true,true]" {
		t.Fatal("Original admitted transports did not settle", source)
	}
	actual := newPeer()
	ingress := NewIngress(nil, NewIngressAuth(env), nil)
	defer ingress.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ingress.Read(ctx, "actual-clear-ingress", actual.server.URL+"/ingress/actual"); err != nil {
		t.Fatal(err)
	}
	entry := func(id string) protocol.JSONObject {
		return object(raw(map[string]any{"type": "user", "uuid": id, "message": map[string]any{"role": "user", "content": "actual admitted transport"}}))
	}
	completed := make(chan error, 1)
	go func() {
		_, err := ingress.Append(ctx, "actual-clear-ingress", actual.server.URL+"/ingress/actual", entry("old-admitted"))
		completed <- err
	}()
	select {
	case <-actual.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ingress.ClearAllSessions()
	if ok, err := ingress.Append(ctx, "actual-clear-ingress", actual.server.URL+"/ingress/actual", entry("new-after-clear")); err != nil || !ok {
		t.Fatal("New sequential wrapper waited for old transport", ok, err)
	}
	actual.once.Do(func() { close(actual.release) })
	if err := <-completed; err != nil {
		t.Fatal("Old admitted append was cancelled by cache clear", err)
	}
	if ok, err := ingress.Append(ctx, "actual-clear-ingress", actual.server.URL+"/ingress/actual", entry("third-after-old-ack")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	old.mu.Lock()
	want := append([]map[string]string{}, old.trace...)
	old.mu.Unlock()
	actual.mu.Lock()
	got := append([]map[string]string{}, actual.trace...)
	actual.mu.Unlock()
	if len(want) != 4 || want[1]["last"] != "seed-head" || want[2]["last"] != "" || want[3]["last"] != "old-admitted" || !reflect.DeepEqual(got, want) {
		t.Fatal("Actual shared head/independent sequence differs original Source", got, want)
	}
}
