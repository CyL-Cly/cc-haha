package clibootstrap

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualInProcessRetryWaitMailboxWakesCancelParksAndNonTransientNeverPolls(t *testing.T) {
	for _, scenario := range []string{"mail-wakes", "cancel-retry", "non-transient"} {
		t.Run(scenario, func(t *testing.T) {
			var posts atomic.Int32
			diagnostics := &inProcessDiagnosticBuffer{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := posts.Add(1)
				if n == 1 {
					w.Header().Set("Content-Type", "application/json")
					status := 503
					detail := "service unavailable transient transport failure"
					if scenario == "non-transient" {
						status = 401
						detail = "invalid api key"
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"` + detail + `"}}`))
					return
				}
				if scenario != "mail-wakes" || n != 2 {
					t.Error("retry cancellation/auth error invoked model", n, scenario)
					http.Error(w, "unexpected", 500)
					return
				}
				if !strings.Contains(string(body), "ACTUAL_MAIL_INTERRUPTS_RETRY") || strings.Contains(string(body), "automatic retry 1 of") {
					t.Error("retry did not prefer actual mailbox input")
				}
				emitPlanFixture(w, 2, "", "ACTUAL_MAIL_RETRY_RECOVERY")
			}))
			defer peer.Close()
			parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
				n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			}, func(o *MainPrintOptions) {
				o.Error = diagnostics
				o.Arguments.Tools = []string{"Agent,TeamCreate,SendMessage,Read"}
				o.NativeInProcessAutoContinueDelays = []time.Duration{5 * time.Second}
			})
			callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"retry-fixture"}`)
			spawn := callActualTeamTool(t, parent, "Agent", `{"name":"worker","prompt":"actual initial request","description":"actual retry fixture"}`)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			lookup := func() *sessioncontroller.BackgroundTask {
				t.Helper()
				task, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
					rows, err := parent.Controller.BackgroundTasksOnOwner(scope)
					if err != nil {
						return nil, err
					}
					for _, row := range rows {
						identity, _ := row.Object("identity")
						if identity.String("agentId") == spawn.String("agent_id") {
							return parent.Controller.BackgroundTaskOnOwner(scope, row.String("id"))
						}
					}
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return task
			}
			t.Logf("spawn %s", rawMain(spawn))
			var task *sessioncontroller.BackgroundTask
			retryIsReady := func() bool {
				ready, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (bool, error) {
					file := parent.teamLeader.config.Read("retry-fixture")
					if file == nil {
						return false, nil
					}
					for _, member := range taskBlocks(file.Get("members")) {
						if member.String("name") == "worker" {
							return string(member.Get("isActive")) == "false", nil
						}
					}
					return false, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return ready
			}
			for ctx.Err() == nil {
				task = lookup()
				if task != nil && posts.Load() == 1 && (scenario == "non-transient" && string(task.Data.Get("isIdle")) == "true" || scenario != "non-transient" && task.WorkCancel != nil && strings.Contains(string(task.Data.Get("messages")), "isApiErrorMessage") && retryIsReady()) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if ctx.Err() != nil {
				parent.outputMu.Lock()
				frames := append([][]byte{}, parent.outputs...)
				parent.outputMu.Unlock()
				for _, data := range frames {
					t.Log(string(data))
				}
				t.Fatal("runtime did not preserve failed teammate lifecycle", ctx.Err(), posts.Load(), task, diagnostics.String())
			}
			if scenario == "mail-wakes" {
				out, _ := taskToolCall(t, parent, "SendMessage", `{"to":"worker","message":"ACTUAL_MAIL_INTERRUPTS_RETRY","summary":"wake actual retry"}`)
				if string(out.Get("success")) != "true" {
					t.Fatal(out)
				}
			}
			if scenario == "cancel-retry" {
				if interrupted, err := parent.InterruptTeammateWork(ctx, task.Data.String("id")); err != nil || !interrupted {
					t.Fatal(interrupted, err)
				}
			}
			for ctx.Err() == nil {
				task = lookup()
				if task != nil && string(task.Data.Get("isIdle")) == "true" && (scenario != "mail-wakes" || posts.Load() == 2) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if ctx.Err() != nil {
				t.Fatal("real wait did not settle", posts.Load())
			}
			if err := parent.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if scenario == "mail-wakes" {
				want = 2
			}
			if posts.Load() != want {
				t.Fatal("actual wait busy-polled provider", posts.Load())
			}
			parent.outputMu.Lock()
			frames := append([][]byte{}, parent.outputs...)
			parent.outputMu.Unlock()
			if strings.Contains(string(rawMain(frames)), "capability is not migrated") {
				t.Fatal("retry silently abandoned native graph")
			}
		})
	}
}

type inProcessDiagnosticBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *inProcessDiagnosticBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(data)
}
func (b *inProcessDiagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
