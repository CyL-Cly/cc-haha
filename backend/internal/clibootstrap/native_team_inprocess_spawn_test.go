package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
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

func TestActualAgentNonReviewSpawnRegistersOriginalTaskRunsPhysicalToolsAndShutdownBookend(t *testing.T) {
	var posts, permissions atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var request struct {
			Tools    []protocol.JSONObject
			Messages []protocol.JSONObject
		}
		_ = json.Unmarshal(data, &request)
		for _, message := range request.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual child tool error %s", rawMain(block))
				}
			}
		}
		if !strings.Contains(string(data), "# Agent Teammate Communication") {
			t.Error("actual system missing")
		}
		switch posts.Add(1) {
		case 1:
			emitNativeTaskTool(w, "actual-team-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_PUBLIC_TEAM_SPAWN"})
		case 2:
			emitNativeTaskTool(w, "actual-team-read", "Read", map[string]any{"file_path": target})
		case 3:
			emitNativeTaskTool(w, "actual-team-send", "SendMessage", map[string]any{"to": "team-lead", "message": "actual artifact completed", "summary": "physical artifact"})
		case 4:
			emitPlanFixture(w, 4, "", "ACTUAL_PUBLIC_TEAM_DONE")
		default:
			t.Error("unexpected extra child POST")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.PermissionMode = "default"
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Read,Write,SendMessage,TaskCreate,TaskGet,TaskList,TaskUpdate"}
		o.ResolvePermission = func(_ context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			if tool.Name == "Write" {
				permissions.Add(1)
			}
			return queryloop.ToolPermission{Behavior: "allow", UpdatedInput: input}, nil
		}
	})
	target = filepath.Join(parent.workingDirectory(), "actual-public-team.txt")
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-public-spawn"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var spawn protocol.JSONObject
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, err := parent.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.Name != "Agent" {
				continue
			}
			input := taskObject(t, []byte(`{"name":"worker","prompt":"create and verify the real artifact then tell team-lead","description":"actual public spawn"}`))
			future, err := tool.StartCallJSON(ctx, input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("actual-spawn-parent"), Opaque: &builtinwrite.CallContext{Scope: scope}})
			if err != nil {
				return err
			}
			result, err := future.Await(scope)
			if err != nil {
				return err
			}
			if err = spawn.UnmarshalJSON(result.Data); err != nil {
				return err
			}
			mapped, err := builtintools.MapResult("Agent", result.Data, quoteMain("actual-spawn-parent"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(rawMain(mapped)), "Spawned successfully.") {
				t.Error("original Agent spawned mapper missing", mapped)
			}
			return nil
		}
		return mainCap("actual Agent tool")
	}); err != nil {
		t.Fatal(err)
	}
	if spawn.String("status") != "teammate_spawned" || spawn.String("agent_id") != "worker@actual-public-spawn" || spawn.String("color") != "blue" {
		t.Fatal("not original spawn result", spawn)
	}
	taskID := ""
	for ctx.Err() == nil {
		idle, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (bool, error) {
			tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
			if err != nil {
				return false, err
			}
			for _, task := range tasks {
				if task.String("type") == "in_process_teammate" {
					taskID = task.String("id")
					if task.String("description") != "worker: create and verify the real artifact then tell team..." || string(task.Get("permissionMode")) != `"default"` || string(task.Get("toolUseId")) != `"actual-spawn-parent"` {
						t.Errorf("original registered task description=%q mode=%s toolUseId=%s", task.String("description"), task.Get("permissionMode"), task.Get("toolUseId"))
					}
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
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("spawn child never idle", posts.Load())
	}
	if posts.Load() != 4 || permissions.Load() != 1 {
		t.Fatal("actual shared child/parent permissions", posts.Load(), permissions.Load())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "ACTUAL_PUBLIC_TEAM_SPAWN" {
		t.Fatal("physical artifact", string(data), err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: parent.taskStore})
	if err != nil {
		t.Fatal(err)
	}
	envelopes := mailbox.Read("team-lead", "actual-public-spawn")
	if len(envelopes) < 2 {
		t.Fatal("physical message and idle envelopes missing", envelopes)
	}
	if err = parent.ShutdownSession(ctx); err != nil {
		t.Fatal("actual task cancel/join failed", err)
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	started, ended := 0, 0
	for _, data := range frames {
		var frame protocol.JSONObject
		if frame.UnmarshalJSON(data) != nil || frame.String("task_id") != taskID {
			continue
		}
		if frame.String("owner_agent_id") != "worker@actual-public-spawn" {
			t.Fatal("original ownership", frame)
		}
		switch frame.String("subtype") {
		case "task_started":
			started++
		case "task_notification":
			ended++
			if frame.String("status") != "completed" || frame.String("output_file") != "" {
				t.Fatal("original final bookend", frame)
			}
		}
	}
	if started != 1 || ended != 1 {
		t.Fatal("unbalanced actual SDK task bookends", started, ended)
	}
}
