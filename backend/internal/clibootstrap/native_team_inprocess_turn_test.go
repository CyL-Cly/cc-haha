package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
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

func TestActualInProcessTeammateTwoFreshSidechainsShareTasksMessagesHistoryAndParentPermissions(t *testing.T) {
	var posts atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		round := posts.Add(1)
		if !strings.Contains(string(data), "# Agent Teammate Communication") {
			t.Error("missing actual teammate system producer")
		}
		var body struct {
			Tools    []protocol.JSONObject
			Messages []protocol.JSONObject
		}
		_ = json.Unmarshal(data, &body)
		found := map[string]bool{}
		for _, tool := range body.Tools {
			found[tool.String("name")] = true
		}
		if !found["TaskCreate"] || !found["SendMessage"] || found["AskUserQuestion"] || found["TeamCreate"] || found["Agent"] {
			t.Errorf("wrong original async teammate tool set %v", found)
		}
		for _, message := range body.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual teammate tool failed %s", rawMain(block))
				}
			}
		}
		if round >= 5 && !strings.Contains(string(data), "FIRST_TEAMMATE_TURN_OK") {
			t.Error("second sidechain lost accumulated first prompt history")
		}
		switch round {
		case 1:
			emitNativeTaskTool(w, "team-create-task", "TaskCreate", map[string]any{"subject": "actual teammate shared task", "description": "actual two prompt chain"})
		case 2:
			emitNativeTaskTool(w, "team-write", "Write", map[string]any{"file_path": target, "content": "actual-inprocess-artifact"})
		case 3:
			emitNativeTaskTool(w, "team-send-first", "SendMessage", map[string]any{"to": "team-lead", "message": "first actual turn", "summary": "first"})
		case 4:
			emitPlanFixture(w, int(round), "", "FIRST_TEAMMATE_TURN_OK")
		case 5:
			emitNativeTaskTool(w, "team-get-task", "TaskGet", map[string]any{"taskId": "1"})
		case 6:
			emitNativeTaskTool(w, "team-read", "Read", map[string]any{"file_path": target})
		case 7:
			emitNativeTaskTool(w, "team-send-next", "SendMessage", map[string]any{"to": "team-lead", "message": "second actual turn", "summary": "second"})
		case 8:
			emitPlanFixture(w, int(round), "", "SECOND_TEAMMATE_TURN_OK")
		default:
			t.Errorf("unexpected POST %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	var parent *MainPrint
	permissionCalls := 0
	parent = backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
		n.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "unrelated-list"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write,TaskCreate,TaskGet,TaskUpdate,TaskList,SendMessage"}
		o.Arguments.PermissionMode = "default"
		o.ResolvePermission = func(_ context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			permissionCalls++
			info, _ := call.Opaque.(*builtinwrite.CallContext)
			if !scope.BelongsTo(parent.Owner) || tool.Name != "Write" || info == nil || info.AgentID == "" || strings.Contains(info.AgentID, "@") {
				return queryloop.ToolPermission{}, fmt.Errorf("foreign or wrong logical permission identity %s %#v", tool.Name, info)
			}
			return queryloop.ToolPermission{Behavior: "allow", Input: in}, nil
		}
	})
	target = filepath.Join(parent.options.Native.CWD, "inprocess-produced.txt")
	identity := sessioncontroller.InProcessTeammateIdentity{TeamName: "MyTeam_", AgentName: "worker", AgentID: "worker@MyTeam_", ParentSessionID: parent.Controller.SessionID(), Color: "blue"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first, err := parent.runNativeInProcessTurn(ctx, nativeInProcessTurn{Identity: identity, Prompt: teamspawn.TeammateMessage("team-lead", "perform actual task", "", "first"), PermissionMode: "default"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := parent.runNativeInProcessTurn(ctx, nativeInProcessTurn{Identity: identity, Prompt: teamspawn.TeammateMessage("team-lead", "continue same teammate", "", "next"), PermissionMode: "default", History: first.Messages})
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 8 || first.AgentID == "" || first.AgentID == second.AgentID || first.Transcript == second.Transcript || permissionCalls != 1 {
		t.Fatal("fresh sidechain/actual permission lifecycle", posts.Load(), permissionCalls, first.AgentID, second.AgentID)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "actual-inprocess-artifact" {
		t.Fatal(string(data), err)
	}
	for _, run := range []nativeInProcessTurnResult{first, second} {
		data, err := os.ReadFile(run.Transcript)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"isSidechain":true`) || !strings.Contains(string(data), `"agentId":"`+run.AgentID+`"`) || strings.Contains(string(data), `"teamName"`) || strings.Contains(string(data), `"agentName"`) {
			t.Fatalf("Source physical sidechain identity %s", data)
		}
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, err := parent.taskStore.List("myteam-")
		if err != nil || len(tasks) != 1 {
			return fmt.Errorf("actual canonical team tasks %v %v", tasks, err)
		}
		accounting, err := parent.Controller.ModelAccountingFor(parent.Owner)
		if err != nil {
			return err
		}
		usage, err := accounting.Snapshot(scope)
		if err != nil {
			return err
		}
		if string(usage.Get("totalInputTokens")) != "8" || string(usage.Get("totalOutputTokens")) != "8" {
			return fmt.Errorf("actual child accounting missing %s", rawMain(usage))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inbox, err := os.ReadFile(filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "teams", "MyTeam_", "inboxes", "team-lead.json"))
	var envelopes []protocol.JSONObject
	if err == nil {
		err = json.Unmarshal(inbox, &envelopes)
	}
	if err != nil || len(envelopes) != 2 || envelopes[1].String("from") != "worker" || envelopes[1].String("color") != "blue" || envelopes[1].String("text") != "second actual turn" {
		t.Fatalf("actual physical teammate mailbox %s %v", inbox, err)
	}
}
