package remoteio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPhysicalRemoteInputParentCancellationClosesReaderAndJoinsTransport(t *testing.T) {
	for _, kind := range []Kind{WebSocket, Hybrid, CCR} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			connected, peerClosed := make(chan struct{}), make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/events/stream") {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, ":owned connection\n\n")
					w.(http.Flusher).Flush()
					close(connected)
					<-r.Context().Done()
					close(peerClosed)
					return
				}
				if r.Header.Get("Upgrade") != "websocket" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"worker":{"worker_epoch":1},"data":[]}`)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				close(connected)
				conn.Read(context.Background())
				close(peerClosed)
			}))
			defer peer.Close()
			env := map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-cancel-owned"}
			url := "ws" + strings.TrimPrefix(peer.URL, "http") + "/ws/cancel"
			if kind == Hybrid {
				env["CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2"] = "1"
			} else if kind == CCR {
				env["CLAUDE_CODE_USE_CCR_V2"] = "1"
				url = peer.URL + "/v1/code/sessions/cancel"
			}
			service, err := New(ctx, Options{URL: url, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			service.Start()
			select {
			case <-connected:
			case <-time.After(3 * time.Second):
				t.Fatal("physical transport did not connect")
			}
			readDone := make(chan error, 1)
			go func() { _, err := service.Read(make([]byte, 1)); readDone <- err }()
			cancel()
			select {
			case err := <-readDone:
				if err != io.EOF {
					t.Fatal("canceled reader did not end", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("parent cancellation stranded owned reader")
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-peerClosed:
			case <-time.After(3 * time.Second):
				t.Fatal("canceled owned transport retained physical connection")
			}
		})
	}
}
