package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualClearDuringBackgroundCreationRetryPreservesClientCacheAndFreshMetadata(t *testing.T) {
	for _, status := range []int{500, 401} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var rootCalls, childCalls atomic.Int32
			var mu sync.Mutex
			var headers, metadata []string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "CLEAR_RETRY_CHILD_PROMPT") && !strings.Contains(string(body), "CLEAR_RETRY_ROOT_PROMPT") {
					mu.Lock()
					headers = append(headers, r.Header.Get("X-Claude-Code-Session-Id"))
					encoded := obj(obj(body).Get("metadata")).String("user_id")
					metadata = append(metadata, obj([]byte(encoded)).String("session_id"))
					mu.Unlock()
					if childCalls.Add(1) == 1 {
						close(started)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"private creation retry after clear"}}`))
						return
					}
					response(w, "clear-retry-child", "", nil, "ACTUAL_CHILD_RETRY_AFTER_CLEAR_COMPLETE")
					return
				}
				if rootCalls.Add(1) == 1 {
					response(w, "clear-retry-root-agent", "Agent", map[string]any{"description": "actual clear creation retry", "subagent_type": "general-purpose", "prompt": "CLEAR_RETRY_CHILD_PROMPT Return a short acknowledgement", "run_in_background": true}, "")
					return
				}
				response(w, "clear-retry-root", "", nil, "ROOT_RETRY_FOREGROUND_COMPLETE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			o.ForegroundShellProgress = true
			o.Arguments.Tools = []string{"Agent"}
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				env["CLAUDE_CODE_MAX_RETRIES"] = "1"
				env["CLAUDE_CODE_TMPDIR"] = o.Native.Temp
			}
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			oldSID := s.process.Controller.SessionID()
			if err = s.Submit(ctx, tui.Input{Text: "CLEAR_RETRY_ROOT_PROMPT", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err = s.Submit(ctx, tui.Input{Text: "/clear", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
				t.Fatal(err)
			}
			newSID := s.process.Controller.SessionID()
			var join func(context.Context) error
			if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				tasks, e := s.process.Controller.BackgroundTasksOnOwner(scope)
				if e != nil {
					return e
				}
				for _, task := range tasks {
					if task.String("type") == "local_agent" {
						entry, e := s.process.Controller.BackgroundTaskOnOwner(scope, task.String("id"))
						if e != nil {
							return e
						}
						join = entry.Join
					}
				}
				return nil
			}); err != nil || join == nil {
				t.Fatal("Actual background retry ownership missing", err)
			}
			releaseOnce.Do(func() { close(release) })
			if err = join(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			actualHeaders := append([]string{}, headers...)
			actualMetadata := append([]string{}, metadata...)
			mu.Unlock()
			wantHeader := oldSID
			if status == 401 {
				wantHeader = newSID
			}
			if len(actualHeaders) != 2 || actualHeaders[0] != oldSID || actualHeaders[1] != wantHeader || len(actualMetadata) != 2 || actualMetadata[0] != oldSID || actualMetadata[1] != newSID {
				t.Fatal("Actual original cache/rebuild boundary lost parent state", status, actualHeaders, actualMetadata, oldSID, newSID)
			}
		})
	}
}
