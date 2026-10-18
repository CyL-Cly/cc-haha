package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualRunningAgentDisabledAttachmentGatesRetainCoordinatorMessage(t *testing.T) {
	for _, gate := range []string{"CLAUDE_CODE_DISABLE_ATTACHMENTS", "CLAUDE_CODE_SIMPLE"} {
		t.Run(gate, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var rounds atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := rounds.Add(1)
				if n == 1 {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					emitNativeTaskTool(w, "gated-real-bash", "Bash", map[string]any{"command": "printf ACTUAL_GATED_TOOL_C7A2", "description": "original disabled attachment gate"})
					return
				}
				if strings.Contains(string(body), "ACTUAL_RETAINED_PENDING_C7A2") || !strings.Contains(string(body), "ACTUAL_GATED_TOOL_C7A2") {
					t.Errorf("disabled gate changed actual next tool request %.2000s", body)
				}
				emitPlanFixture(w, int(n), "", "ACTUAL_GATE_COMPLETE_C7A2")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "0"
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment[gate] = "1"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var id string
			if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				request, e := actualBackgroundAgentRequest(scope, m, "Start genuine gated Agent")
				if e != nil {
					return e
				}
				future, e := m.runBackgroundAgent(ctx, scope, request)
				if e != nil {
					return e
				}
				run, e := future.Await(scope)
				id = run.AgentID
				return e
			}); e != nil {
				t.Fatal(e)
			}
			select {
			case <-started:
			case <-ctx.Done():
				var snapshot string
				_ = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					row, e := m.Controller.BackgroundTaskOnOwner(scope, id)
					if row != nil {
						snapshot = string(rawMain(row.Data))
					}
					return e
				})
				t.Fatal(ctx.Err(), snapshot)
			}
			response, e := m.SendAgentMessage(ctx, id, "ACTUAL_RETAINED_PENDING_C7A2")
			if e != nil || response.String("delivery") != "queued" {
				t.Fatal(response, e)
			}
			close(release)
			task := waitActualAgent(t, m, id)
			if task.Data.String("status") != "completed" || string(task.Data.Get("pendingMessages")) != `["ACTUAL_RETAINED_PENDING_C7A2"]` || rounds.Load() != 2 {
				t.Fatal("disabled gate discarded or injected queued message", rawMain(task.Data), rounds.Load())
			}
		})
	}
}
