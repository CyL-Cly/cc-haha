package remoteio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSourceSSETerminalOversizeAndPermanentHTTPClose(t *testing.T) {
	for _, kind := range []string{"oversize-complete", "oversize-incomplete", "401", "403", "404"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			newPeer := func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(kind, "oversize") {
						w.Header().Set("Content-Type", "text/event-stream")
						data := strings.Repeat("x", MaxSSEFrameBytes+1)
						if kind == "oversize-complete" {
							data += "\n\n"
						}
						io.WriteString(w, data)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					var status int
					fmt.Sscan(kind, &status)
					w.WriteHeader(status)
				}))
			}
			env := map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-terminal"}
			source := newPeer()
			defer source.Close()
			want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"url": source.URL + "/sessions/terminal", "env": env, "sseTerminal": true})
			peer := newPeer()
			defer peer.Close()
			closed := make(chan struct{})
			var calls atomic.Int32
			service, err := New(ctx, Options{URL: peer.URL + "/sessions/terminal", Environment: env, OnClose: func(int, error) {
				if calls.Add(1) == 1 {
					close(closed)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			// Exercise the actual SSE transport independently of unrelated CCR
			// initialization requests, as the original transport corpus does.
			service.spawn(service.sseLoop)
			data, err := io.ReadAll(service)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("SSE terminal never closed input")
			}
			var sourceCode *int
			if err := json.Unmarshal(want.Get("code"), &sourceCode); err != nil {
				t.Fatal(err)
			}
			code := 0
			if sourceCode != nil {
				code = *sourceCode
			}
			if service.CloseCode() != code || service.Err() != nil || len(data) != 0 || calls.Load() != 1 {
				t.Fatalf("Source close %s incoming%s; Go close=%d err=%v bytes=%d calls=%d", want.Get("code"), want.Get("incoming"), service.CloseCode(), service.Err(), len(data), calls.Load())
			}
		})
	}
}
