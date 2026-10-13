package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualDesktopStopJoinsInProcessRuntimeAndResumesItsDurableCheckpoint(t *testing.T) {
	var posts atomic.Int32
	entered, canceled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			emitPlanFixture(w, 1, "", "BEFORE_DESKTOP_STOP_CHECKPOINT")
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
			close(canceled)
		case 3:
			if !strings.Contains(string(body), "BEFORE_DESKTOP_STOP_CHECKPOINT") || !strings.Contains(string(body), "resume after desktop Stop") {
				t.Error("actual post-Stop request lost durable own history or new prompt")
			}
			emitPlanFixture(w, 3, "", "AFTER_DESKTOP_STOP_RESUMED")
		default:
			t.Error("unexpected provider request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	var mu sync.Mutex
	var terminal []protocol.JSONObject
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,SendMessage"}
		o.OnOutput = func(data []byte) error {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				row := nativeInProcessObject([]byte(line))
				if row.String("subtype") == "task_notification" && row.String("status") == "failed" {
					mu.Lock()
					terminal = append(terminal, row.Clone())
					mu.Unlock()
				}
			}
			return nil
		}
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"desktop-stop-resume"}`)
	spawn := callActualTeamTool(t, parent, "Agent", `{"name":"worker","prompt":"remember a durable checkpoint","description":"actual Stop member"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	waitIdle := func(round int32) protocol.JSONObject {
		t.Helper()
		for ctx.Err() == nil {
			row, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (protocol.JSONObject, error) {
				rows, err := parent.Controller.BackgroundTasksOnOwner(scope)
				for _, task := range rows {
					identity, _ := task.Object("identity")
					if identity.String("agentId") == spawn.String("agent_id") && task.String("status") == "running" && string(task.Get("isIdle")) == "true" {
						return task.Clone(), err
					}
				}
				return protocol.JSONObject{}, err
			})
			if err != nil {
				t.Fatal(err)
			}
			if row.IsObject() && posts.Load() == round {
				return row
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("real teammate never became idle", posts.Load())
		return protocol.JSONObject{}
	}
	first := waitIdle(1)
	identity, _ := first.Object("identity")
	resumable := identity.String("resumableAgentId")
	var transcript string
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		transcript, _, _, err = parent.Controller.AgentTranscriptSourceOnOwner(scope, resumable)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := parent.SendTeammateUserMessage(ctx, first.String("id"), "begin interruptible actual stream"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("real streaming child never started")
	}
	receipt, err := parent.Owner.SubmitIndependent(ctx, cooperative.Cancel, func(scope *cooperative.Scope) error {
		return parent.pauseNativeInProcessTeammatesOnOwner(scope)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("Stop acknowledged before actual provider stream cancellation")
	}
	if file := parent.teamLeader.config.Read("desktop-stop-resume"); file == nil || !strings.Contains(string(file.Get("members")), `"name":"worker"`) {
		t.Fatal("Stop deleted the resumable team roster")
	}
	data, err := os.ReadFile(transcript)
	if err != nil || !strings.Contains(string(data), "BEFORE_DESKTOP_STOP_CHECKPOINT") {
		t.Fatal("Stop lost physical transcript checkpoint", err)
	}
	mu.Lock()
	if len(terminal) != 1 || terminal[0].String("task_id") != first.String("id") || terminal[0].String("task_type") != "in_process_teammate" || terminal[0].String("owner_agent_id") != spawn.String("agent_id") || terminal[0].String("summary") != first.String("description")+": runtime connection lost; task completion could not be confirmed" {
		t.Errorf("Source runtime-exit failed bookend changed or duplicated: %s", rawMain(terminal))
	}
	mu.Unlock()
	resumed, _ := taskToolCall(t, parent, "SendMessage", `{"to":"worker","message":"resume after desktop Stop","summary":"resume checkpoint"}`)
	if string(resumed.Get("success")) != "true" {
		t.Fatal("actual owner could not resume stopped teammate", resumed)
	}
	second := waitIdle(3)
	secondIdentity, _ := second.Object("identity")
	if secondIdentity.String("resumableAgentId") != resumable || second.String("id") == first.String("id") {
		t.Fatal("Stop resume replaced durable agent identity or reused a terminated task", second)
	}
}

func TestInProcessDesktopStopRejectsForeignOwnerAndReportsActualJoinOrDeliveryFailure(t *testing.T) {
	for _, kind := range []string{"empty", "ordinary-task", "foreign-owner", "join-failure", "delivery-failure", "missing-description", "empty-description"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("physical fixture " + kind)
			var delivered atomic.Int32
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) {
				o.OnOutput = func(data []byte) error {
					if strings.Contains(string(data), `"subtype":"task_notification"`) {
						delivered.Add(1)
						if kind == "missing-description" || kind == "empty-description" {
							row := nativeInProcessObject(data)
							prefix := "fault-worker"
							if kind == "empty-description" {
								prefix = ""
							}
							if row.Get("description") != nil || row.String("summary") != prefix+": runtime connection lost; task completion could not be confirmed" {
								t.Errorf("Source absent-vs-empty description semantics changed: %s", data)
							}
						}
						if kind == "delivery-failure" {
							return failure
						}
					}
					return nil
				}
			})
			if kind == "foreign-owner" {
				other := cooperative.New()
				defer other.Close()
				err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
					return m.pauseNativeInProcessTeammatesOnOwner(scope)
				})
				if !errors.Is(err, cooperative.ErrRestricted) {
					t.Fatal("foreign owner gained access to live teammate state", err)
				}
				return
			}
			worker, cancelWorker := context.WithCancelCause(context.Background())
			defer cancelWorker(context.Canceled)
			var joined atomic.Bool
			err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if kind != "empty" {
					taskKind := "in_process_teammate"
					if kind == "ordinary-task" {
						taskKind = "local_bash"
					}
					row := nativeInProcessObject(rawMain(map[string]any{"id": "fault-worker", "type": taskKind, "status": "running", "messages": []any{}, "description": "fixture", "identity": map[string]string{"agentId": "worker@fixture"}}))
					if kind == "missing-description" {
						row.Delete("description")
					} else if kind == "empty-description" {
						row.Set("description", quoteMain(""))
					}
					if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, &sessioncontroller.BackgroundTask{Data: row, Cancel: cancelWorker, ReadOutput: func(context.Context) (json.RawMessage, error) { return nil, nil }, Join: func(context.Context) error {
						joined.Store(true)
						if worker.Err() == nil {
							return errors.New("join began before actual cancellation")
						}
						if kind == "join-failure" {
							return failure
						}
						return nil
					}}); err != nil {
						return err
					}
				}
				return m.pauseNativeInProcessTeammatesOnOwner(scope)
			})
			if kind == "empty" || kind == "ordinary-task" {
				if err != nil || worker.Err() != nil || joined.Load() || delivered.Load() != 0 {
					t.Fatal("in-process pause touched ordinary tasks or fabricated a notification", err)
				}
			} else if kind == "missing-description" || kind == "empty-description" {
				if err != nil || worker.Err() == nil || !joined.Load() || delivered.Load() != 1 {
					t.Fatal("description compatibility case did not physically cancel/join and deliver", err)
				}
			} else if !errors.Is(err, failure) || worker.Err() == nil || !joined.Load() || (kind == "join-failure" && delivered.Load() != 0) || (kind == "delivery-failure" && delivered.Load() != 1) {
				t.Fatal("Stop hid actual join/delivery failure or acknowledged an unjoined child", err, joined.Load(), delivered.Load())
			}
		})
	}
}
