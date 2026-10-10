package clibootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualKillTeammateCancelsPhysicalShellSettlesWaiterRemovesRosterAndJoinsOnce(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if posts.Add(1) != 1 {
			t.Error("model called after physical kill")
			http.Error(w, "unexpected", 500)
			return
		}
		emitNativeTaskTool(w, "actual-team-kill-shell", "Bash", map[string]any{"command": "sleep 30", "description": "physical cancellable teammate shell"})
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Bash,Read,SendMessage"}
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-kill"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := parent.stageNativeTeamAgent(ctx, scope, "actual-kill", objMain([]byte(`{"name":"worker","prompt":"run a real long shell and wait","description":"physical kill"}`)), queryloop.JSONToolCallContext{ToolUseID: quoteMain("actual-kill-parent"), Opaque: &builtinwrite.CallContext{Scope: scope}})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var task *sessioncontroller.BackgroundTask
	for ctx.Err() == nil {
		var err error
		task, err = inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
			tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
			if err != nil {
				return nil, err
			}
			for _, data := range tasks {
				if data.String("type") == "in_process_teammate" && strings.Contains(string(data.Get("inProgressToolUseIDs")), "actual-team-kill-shell") {
					return parent.Controller.BackgroundTaskOnOwner(scope, data.String("id"))
				}
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if task != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task == nil {
		t.Fatal("actual shell never started")
	}
	woke := make(chan struct{})
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := parent.Controller.StopBackgroundTaskOnOwner(scope, task.Data.String("id"), 1); err == nil || !strings.Contains(err.Error(), "Unsupported task type") {
			t.Error("generic TaskStop differs from original", err)
		}
		return parent.Controller.RegisterInProcessIdleOnOwner(scope, task.Data.String("id"), func() { close(woke) })
	}); err != nil {
		t.Fatal(err)
	}
	killed, err := parent.KillTeammate(ctx, task.Data.String("id"))
	if err != nil || !killed {
		t.Fatal("actual specific kill failed", killed, err)
	}
	select {
	case <-woke:
	case <-ctx.Done():
		t.Fatal("original idle waiter remained blocked")
	}
	if err = task.Join(ctx); err != nil {
		t.Fatal("physical child did not join after kill", err)
	}
	if killed, err = parent.KillTeammate(ctx, task.Data.String("id")); err != nil || killed {
		t.Fatal("terminal task killed twice", killed, err)
	}
	config := parent.teamLeader.config.Read("actual-kill")
	var members []protocol.JSONObject
	_ = json.Unmarshal(config.Get("members"), &members)
	for _, member := range members {
		if member.String("agentId") == "worker@actual-kill" {
			t.Fatal("physical team member remained", member)
		}
	}
	if err = parent.ShutdownSession(ctx); err != nil {
		t.Fatal("production shutdown did not drain killed worker", err)
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	started, terminal := 0, 0
	for _, raw := range frames {
		frame := objMain(raw)
		if frame.String("task_id") != task.Data.String("id") {
			continue
		}
		switch frame.String("subtype") {
		case "task_started":
			started++
		case "task_notification":
			terminal++
			if frame.String("status") != "stopped" || frame.String("summary") != task.Data.String("description") {
				t.Fatal("original stopping bookend", frame)
			}
		}
	}
	if started != 1 || terminal != 1 || posts.Load() != 1 {
		t.Fatal("unbalanced physical task lifecycle", started, terminal, posts.Load())
	}
}
