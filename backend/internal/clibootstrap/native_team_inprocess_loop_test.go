package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
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

func TestActualInProcessLoopReadsParentQueueAndPhysicalMailboxAcrossFreshChildrenAndShutdownJoins(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round := posts.Add(1)
		if round > 1 && !strings.Contains(string(body), "FIRST_ACTUAL_LOOP_TURN") {
			t.Error("continuous teammate lost original history")
		}
		if round == 2 && !strings.Contains(string(body), "Actual queued user continuation") {
			t.Error("real parent pending queue not consumed")
		}
		if round == 3 && !strings.Contains(string(body), "Actual physical peer continuation") {
			t.Error("real inbox not consumed")
		}
		if round > 3 {
			t.Error("unexpected model turn", round)
			http.Error(w, "unexpected", 500)
			return
		}
		result := []string{"FIRST_ACTUAL_LOOP_TURN", "SECOND_ACTUAL_LOOP_TURN", "THIRD_ACTUAL_LOOP_TURN"}[round-1]
		emitPlanFixture(w, int(round), "", result)
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	identity := sessioncontroller.InProcessTeammateIdentity{AgentName: "worker", TeamName: "actual-loop", AgentID: "worker@actual-loop", ParentSessionID: parent.Controller.SessionID(), Color: "blue"}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: parent.taskStore, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Write(identity.TeamName, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(identity.TeamName)}, protocol.JSONField{Name: "leadSessionId", Value: quoteMain(identity.ParentSessionID)}, protocol.JSONField{Name: "createdAt", Value: rawMain(1700000000000)}, protocol.JSONField{Name: "members", Value: rawMain([]map[string]any{{"name": "worker", "agentId": identity.AgentID, "subscriptions": []string{}}})})); err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: parent.taskStore})
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	done := make(chan struct{})
	finished := make(chan error, 1)
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err = parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		data := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain("tactual")}, protocol.JSONField{Name: "type", Value: quoteMain("in_process_teammate")}, protocol.JSONField{Name: "status", Value: quoteMain("running")}, protocol.JSONField{Name: "isIdle", Value: rawMain(false)}, protocol.JSONField{Name: "permissionMode", Value: quoteMain("default")}, protocol.JSONField{Name: "pendingUserMessages", Value: rawMain([]string{})}, protocol.JSONField{Name: "identity", Value: rawMain(map[string]any{"agentId": identity.AgentID, "agentName": identity.AgentName, "teamName": identity.TeamName})})
		if err := parent.Controller.RegisterBackgroundTaskOnOwner(scope, &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) {
			return foregroundTaskRead(filepath.Join(parent.options.Native.Temp, "tactual.output"))
		}, Join: func(ctx context.Context) error {
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}}); err != nil {
			return err
		}
		_, err := parent.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
			cancel(context.Canceled)
			return nativeSkillWork(parent, next, func() (struct{}, error) { <-done; return struct{}{}, nil })
		}))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(done)
		finished <- parent.runNativeInProcessLoop(life, "tactual", identity, nil, "fixture-model", "Initial actual turn", "actual loop")
	}()
	waitIdle := func(round int32) {
		t.Helper()
		for ctx.Err() == nil {
			idle, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (bool, error) {
				task, err := parent.Controller.BackgroundTaskOnOwner(scope, "tactual")
				if err != nil || task == nil {
					return false, err
				}
				return string(task.Data.Get("isIdle")) == "true", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if idle && posts.Load() == round {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("actual loop did not become idle", posts.Load())
	}
	waitIdle(1)
	if err = parent.updateInProcessTask(ctx, "tactual", func(task protocol.JSONObject) (protocol.JSONObject, error) {
		task.Set("pendingUserMessages", rawMain([]string{"Actual queued user continuation"}))
		return task, nil
	}); err != nil {
		t.Fatal(err)
	}
	waitIdle(2)
	if err = mailbox.Write("worker", identity.TeamName, protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain("peer")}, protocol.JSONField{Name: "text", Value: quoteMain("Actual physical peer continuation")}, protocol.JSONField{Name: "timestamp", Value: quoteMain("2026-10-07T00:00:00.000Z")})); err != nil {
		t.Fatal(err)
	}
	waitIdle(3)
	task, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
		return parent.Controller.BackgroundTaskOnOwner(scope, "tactual")
	})
	if err != nil || task == nil {
		t.Fatal(err)
	}
	var displayed []protocol.JSONObject
	if err = json.Unmarshal(task.Data.Get("messages"), &displayed); err != nil {
		t.Fatal(err)
	}
	if len(displayed) != 5 || !strings.Contains(string(rawMain(displayed)), "FIRST_ACTUAL_LOOP_TURN") || !strings.Contains(string(rawMain(displayed)), "Actual physical peer continuation") {
		t.Fatalf("actual UI mirror did not consume original yielded messages %s", rawMain(displayed))
	}
	progress, _ := task.Data.Object("progress")
	if string(progress.Get("tokenCount")) != "2" || string(progress.Get("toolUseCount")) != "0" {
		t.Fatal("progress not actual latest turn", progress)
	}
	if err = parent.ShutdownSession(ctx); err != nil {
		t.Fatal("production cleanup failed to join actual loop", err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 3 {
		t.Fatal("shutdown issued extra provider call", posts.Load())
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	runs := map[string]string{}
	terminals := 0
	for _, data := range frames {
		var event protocol.JSONObject
		if event.UnmarshalJSON(data) != nil || event.String("subtype") != "agent_run_message" {
			continue
		}
		id := event.String("run_agent_id")
		if id == identity.AgentID || event.String("target_agent_id") != identity.AgentID || event.String("target_agent_scope_id") != `["actual-loop","`+identity.ParentSessionID+`",1700000000000]` {
			t.Fatal("actual original per-run/outer identity routing", event)
		}
		if previous := runs[id]; previous != "" && previous != event.String("stream_id") {
			t.Fatal("stream identity changed midturn")
		}
		runs[id] = event.String("stream_id")
		if event.String("event_kind") == "complete" {
			terminals++
		}
	}
	if len(runs) != 3 || terminals != 3 {
		t.Fatal("actual child message/terminal stream not complete", len(runs), terminals)
	}
	team := config.Read(identity.TeamName)
	var members []protocol.JSONObject
	_ = json.Unmarshal(team.Get("members"), &members)
	if len(members) != 1 || string(members[0].Get("isActive")) != "false" {
		t.Fatal("idle roster not persisted", team)
	}
	entries, err := os.ReadDir(filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"))
	if err != nil || len(entries) == 0 {
		t.Fatal("actual sidechain project missing", err)
	}
	directory := filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", entries[0].Name(), identity.ParentSessionID, "subagents")
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	transcripts := 0
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".jsonl") {
			transcripts++
			data, err := os.ReadFile(filepath.Join(directory, file.Name()))
			if err != nil || !strings.Contains(string(data), "FIRST_ACTUAL_LOOP_TURN") {
				t.Fatal("fresh child lost initial history", file.Name(), err)
			}
		}
	}
	if transcripts != 3 {
		t.Fatal(fmt.Sprintf("expected 3 actual per-turn sidechains, got %d", transcripts))
	}
}
