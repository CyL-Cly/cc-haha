package remoteio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

type hybridClosePeer struct {
	server                             *httptest.Server
	entered, released, socketClosed    chan struct{}
	enterOnce, releaseOnce, socketOnce sync.Once
	mu                                 sync.Mutex
	ids                                []string
}

func newHybridClosePeer(t *testing.T, ctx context.Context) *hybridClosePeer {
	p := &hybridClosePeer{entered: make(chan struct{}), released: make(chan struct{}), socketClosed: make(chan struct{})}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first-post-entered":
			select {
			case <-p.entered:
			case <-r.Context().Done():
			}
			return
		case "/socket-closed":
			select {
			case <-p.socketClosed:
			case <-r.Context().Done():
			}
			return
		case "/release-post":
			p.releaseOnce.Do(func() { close(p.released) })
			return
		}
		if r.Method == http.MethodPost {
			var body struct{ Events []protocol.JSONObject }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			p.enterOnce.Do(func() { close(p.entered) })
			select {
			case <-p.released:
			case <-r.Context().Done():
				return
			}
			p.mu.Lock()
			for _, event := range body.Events {
				p.ids = append(p.ids, event.String("uuid"))
			}
			p.mu.Unlock()
			io.WriteString(w, `{}`)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(ctx)
		p.socketOnce.Do(func() { close(p.socketClosed) })
	}))
	t.Cleanup(func() { p.releaseOnce.Do(func() { close(p.released) }); p.server.Close() })
	return p
}

func TestActualSourceHybridCloseDrainsAdmittedInFlightAndQueuedPosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env := map[string]string{"CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2": "1", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-close"}
	messages := []protocol.JSONObject{object([]byte(`{"type":"assistant","uuid":"in-flight","message":{"role":"assistant","content":"first"}}`)), object([]byte(`{"type":"result","uuid":"queued","result":"second"}`))}
	source := newHybridClosePeer(t, ctx)
	runSourceFixture(t, "native-remoteio-ws.fixture.ts", map[string]any{"url": strings.Replace(source.server.URL, "http", "ws", 1) + "/ws/close", "env": env, "messages": messages, "closeQueued": true})
	peer := newHybridClosePeer(t, ctx)
	connected := make(chan struct{})
	service, err := New(ctx, Options{URL: strings.Replace(peer.server.URL, "http", "ws", 1) + "/ws/close", Environment: env, OnConnect: func() { close(connected) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Start()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- service.WriteMessage(messages[0]) }()
	select {
	case <-peer.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { second <- service.WriteMessage(messages[1]) }()
	for {
		service.hybrid.mu.Lock()
		queued := len(service.hybrid.pending) > 0
		service.hybrid.mu.Unlock()
		if queued {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	select {
	case <-peer.socketClosed:
	case <-ctx.Done():
		t.Fatal("close did not retire actual socket")
	}
	peer.releaseOnce.Do(func() { close(peer.released) })
	select {
	case err = <-closed:
	case <-ctx.Done():
		t.Fatal("close did not join admitted POSTs")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{first, second} {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("closed write waiter leaked")
		}
	}
	source.mu.Lock()
	want := append([]string(nil), source.ids...)
	source.mu.Unlock()
	peer.mu.Lock()
	got := append([]string(nil), peer.ids...)
	peer.mu.Unlock()
	if !reflect.DeepEqual(want, []string{"in-flight", "queued"}) || !reflect.DeepEqual(got, want) {
		t.Fatalf("Source close delivered %v; Go close delivered %v", want, got)
	}
}
