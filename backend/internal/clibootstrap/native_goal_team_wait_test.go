package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualGoalAuxiliaryHookEndsLeadTurnWhileRealTeammateWorksAndRechecksReport(t *testing.T) {
	parked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mainCalls, evaluations, leaderWake atomic.Int32
	diagnostics := &inProcessDiagnosticBuffer{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := string(data)
		if strings.Contains(body, "You are evaluating a hook") {
			n := evaluations.Add(1)
			response := `{"ok":false,"reason":"the real teammate is still working"}`
			if n > 1 {
				if !strings.Contains(body, "ACTUAL_TEAMMATE_GOAL_REPORT") {
					t.Error("goal recheck lost actual member report")
				}
				response = `{"ok":true}`
			}
			emitPlanFixture(w, int(n)+10, "", response)
			return
		}
		if strings.Contains(body, "CHILD_WORK_PENDING") && !strings.Contains(body, "ROOT_GOAL_WORK") {
			close(parked)
			select {
			case <-release:
				emitPlanFixture(w, 1, "", "ACTUAL_TEAMMATE_GOAL_REPORT")
			case <-r.Context().Done():
			}
			return
		}
		n := mainCalls.Add(1)
		if n > 2 {
			t.Error("lead busy-polled or continued goal after success", n)
		}
		emitPlanFixture(w, int(n)+2, "", "lead awaits actual teammate report")
	}))
	defer peer.Close()
	defer once.Do(func() { close(release) })
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-evaluator"
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,SendMessage,Read"}
		o.Error = diagnostics
		o.OnLeaderInboxReady = func() error { leaderWake.Add(1); return nil }
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-goal-wait"}`)
	spawn := callActualTeamTool(t, parent, "Agent", `{"name":"worker","prompt":"CHILD_WORK_PENDING","description":"actual child work"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("actual teammate did not begin request")
	}
	user := func(text string) protocol.JSONObject {
		return objMain(rawMain(map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": text}}))
	}
	done := make(chan error, 1)
	go func() { done <- parent.RunTurn(ctx, user("/goal ROOT_GOAL_WORK")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		once.Do(func() { close(release) })
		parent.Close()

		t.Fatal("goal forced lead to stay busy instead of ending turn")
	}
	if mainCalls.Load() != 1 || evaluations.Load() != 1 || leaderWake.Load() != 1 {
		t.Fatal("waiting goal invoked extra full-context model turns", mainCalls.Load(), evaluations.Load())
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, err := parent.nativeGoalState(scope)
		if err != nil {
			return err
		}
		goal, err := state.Get(scope, parent.Controller.SessionID())
		if err == nil && goal == nil {
			t.Error("waiting incorrectly completed goal")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	found := false
	for _, frame := range frames {
		if strings.Contains(string(frame), "Goal waiting for teammates: the real teammate is still working") {
			found = true
		}
	}
	if !found {
		t.Fatal("actual SDK output missed goal waiting status")
	}
	once.Do(func() { close(release) })
	for ctx.Err() == nil {
		idle, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (bool, error) {
			tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
			if err != nil {
				return false, err
			}
			for _, task := range tasks {
				identity, _ := task.Object("identity")
				if identity.String("agentId") == spawn.String("agent_id") {
					return string(task.Get("isIdle")) == "true", nil
				}
			}
			return false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if idle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("member never delivered actual report")
	}
	if active, err := parent.RunLeaderInboxCycle(ctx); err != nil || !active {
		t.Fatal("actual desktop continuation did not consume member report", active, err, diagnostics.String())
	}
	if mainCalls.Load() != 2 || evaluations.Load() != 2 {
		t.Fatal("member report failed to recheck goal exactly once", mainCalls.Load(), evaluations.Load())
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, err := parent.nativeGoalState(scope)
		if err != nil {
			return err
		}
		goal, err := state.Get(scope, parent.Controller.SessionID())
		if err == nil && goal != nil {
			t.Error("completed member report retained goal")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
