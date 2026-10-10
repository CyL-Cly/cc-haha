package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualInProcessEscapeCancelsCurrentPhysicalShellKeepsTeammateAndAcceptsUserContinuation(t *testing.T) {
	var posts atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			emitNativeTaskTool(w, "actual-interrupt-work", "Bash", map[string]any{"command": "sleep 30", "description": "real cancellable teammate work"})
		case 2:
			if strings.Contains(string(body), "actual-interrupt-work") {
				t.Error("uncompleted tool envelope was not dropped by original fork filter")
			}
			if !strings.Contains(string(body), "Actual user continuation after Escape") {
				t.Error("actual user queue not consumed")
			}
			emitNativeTaskTool(w, "continued-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_CONTINUED_TEAMMATE"})
		case 3:
			emitPlanFixture(w, 3, "", "ACTUAL_WORK_CANCEL_CONTINUED")
		default:
			t.Error("unexpected model call")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Bash,Write,Read,SendMessage"}
	})
	target = filepath.Join(parent.workingDirectory(), "continued-teammate.txt")
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-work-cancel"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		input := taskObject(t, []byte(`{"name":"worker","prompt":"Run real work and then wait","description":"cancellable"}`))
		future, err := parent.stageNativeTeamAgent(ctx, scope, "actual-work-cancel", input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("actual-work-parent"), Opaque: &builtinwrite.CallContext{Scope: scope}})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	taskID := ""
	wait := func(predicate func(protocol.JSONObject) bool) protocol.JSONObject {
		t.Helper()
		for ctx.Err() == nil {
			var found protocol.JSONObject
			_, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (struct{}, error) {
				tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
				if err != nil {
					return struct{}{}, err
				}
				for _, task := range tasks {
					if task.String("type") == "in_process_teammate" {
						taskID = task.String("id")
						if predicate(task) {
							found = task
						}
					}
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if found.IsObject() {
				return found
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("actual task state timeout", posts.Load())
		return protocol.JSONObject{}
	}
	wait(func(task protocol.JSONObject) bool {
		return strings.Contains(string(task.Get("inProgressToolUseIDs")), "actual-interrupt-work")
	})
	interrupted, err := parent.InterruptTeammateWork(ctx, taskID)
	if err != nil || !interrupted {
		t.Fatal("actual work cancellation unavailable", interrupted, err)
	}
	task := wait(func(task protocol.JSONObject) bool { return string(task.Get("isIdle")) == "true" })
	if task.String("status") != "running" || !strings.Contains(string(task.Get("messages")), "[Request interrupted by user]") {
		t.Fatal("Escape killed teammate or omitted original UI interruption", task.String("status"))
	}
	if posts.Load() != 1 {
		t.Fatal("Escape prompted another model", posts.Load())
	}
	if err = parent.SendTeammateUserMessage(ctx, taskID, "Actual user continuation after Escape"); err != nil {
		t.Fatal(err)
	}
	task = wait(func(task protocol.JSONObject) bool { return posts.Load() == 3 && string(task.Get("isIdle")) == "true" })
	if !strings.Contains(string(task.Get("messages")), "Actual user continuation after Escape") {
		t.Fatal("user input missing from actual UI mirror")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "ACTUAL_CONTINUED_TEAMMATE" {
		t.Fatal("actual continuation artifact", err, string(data))
	}
	if err = parent.ShutdownSession(ctx); err != nil {
		t.Fatal("actual lifecycle cleanup did not join", err)
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	cancelled, complete := 0, 0
	for _, data := range frames {
		var event protocol.JSONObject
		if event.UnmarshalJSON(data) == nil && event.String("subtype") == "agent_run_message" {
			if event.String("event_kind") == "cancelled" {
				cancelled++
			}
			if event.String("event_kind") == "complete" {
				complete++
			}
		}
	}
	if cancelled != 1 || complete != 1 {
		t.Fatal("actual per-turn stream bookends", cancelled, complete)
	}
}
