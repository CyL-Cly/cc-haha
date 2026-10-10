package clibootstrap

import (
	"context"
	"encoding/json"
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

func TestActualStructuredShutdownMailboxWakeReplyPersistThenInProcessExitWithoutExtraModel(t *testing.T) {
	var posts atomic.Int32
	var requestID atomic.Value
	requestID.Store("")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			emitPlanFixture(w, 1, "", "INITIAL_TRUE_TEAM_IDLE")
		case 2:
			if !strings.Contains(string(body), requestID.Load().(string)) || !strings.Contains(string(body), "shutdown_request") {
				t.Error("original physical shutdown request not delivered")
			}
			emitNativeTaskTool(w, "actual-shutdown-reply", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": requestID.Load().(string), "approve": true}})
		default:
			t.Error("shutdown requested another model POST")
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
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,SendMessage,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"actual-protocol-loop"}`)
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := parent.stageNativeTeamAgent(ctx, scope, "actual-protocol-loop", taskObject(t, []byte(`{"name":"worker","prompt":"wait for instructions","description":"true shutdown"}`)), queryloop.JSONToolCallContext{ToolUseID: quoteMain("actual-protocol-parent"), Opaque: &builtinwrite.CallContext{Scope: scope}})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	idle := false
	for ctx.Err() == nil {
		_, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (struct{}, error) {
			tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
			for _, task := range tasks {
				if task.String("type") == "in_process_teammate" && string(task.Get("isIdle")) == "true" {
					idle = true
				}
			}
			return struct{}{}, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if idle {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !idle {
		t.Fatal("actual child never idle")
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := parent.sendNativeStructuredTeamMessage(ctx, scope, taskObject(t, []byte(`{"to":"worker","message":{"type":"shutdown_request","reason":"user requested closure"}}`)))
		if err != nil {
			return err
		}
		result, err := future.Await(scope)
		if err == nil {
			data := taskObject(t, result.Data)
			requestID.Store(data.String("request_id"))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		tasks, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
			return parent.Controller.BackgroundTasksOnOwner(scope)
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range tasks {
			if task.String("type") == "in_process_teammate" {
				found = true
			}
		}
		if !found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("approved shutdown did not settle actual task")
	}
	if posts.Load() != 2 {
		t.Fatal("actual shutdown extra POST", posts.Load())
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: parent.taskStore})
	if err != nil {
		t.Fatal(err)
	}
	var rows []protocol.JSONObject
	_ = json.Unmarshal(mailbox.Read("team-lead", "actual-protocol-loop"), &rows)
	approved := false
	for _, row := range rows {
		var payload protocol.JSONObject
		if payload.UnmarshalJSON([]byte(row.String("text"))) == nil && payload.String("type") == "shutdown_approved" && payload.String("requestId") == requestID.Load().(string) {
			approved = true
			if payload.String("backendType") != "in-process" || payload.String("paneId") != "in-process" {
				t.Fatal("real self config metadata missing", payload)
			}
		}
	}
	if !approved {
		t.Fatal("actual shutdown approval was not written")
	}
	if err = parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	parent.outputMu.Lock()
	frames := append([][]byte{}, parent.outputs...)
	parent.outputMu.Unlock()
	toolReply, ended := 0, 0
	for _, data := range frames {
		var event protocol.JSONObject
		if event.UnmarshalJSON(data) != nil {
			continue
		}
		if event.String("subtype") == "task_notification" {
			ended++
		}
		if event.String("subtype") == "agent_run_message" {
			message, _ := event.Object("message")
			if message.String("type") == "user" && strings.Contains(string(message.Get("message")), "actual-shutdown-reply") {
				toolReply++
			}
		}
	}
	if toolReply != 1 || ended != 1 {
		t.Fatal("actual reply and task closing bookend", toolReply, ended)
	}
	entries, err := os.ReadDir(filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"))
	if err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, entry := range entries {
		directory := filepath.Join(parent.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", entry.Name(), parent.Controller.SessionID(), "subagents")
		files, _ := os.ReadDir(directory)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".jsonl") {
				data, _ := os.ReadFile(filepath.Join(directory, file.Name()))
				if strings.Contains(string(data), "Shutdown approved. Sent confirmation") {
					persisted = true
				}
			}
		}
	}
	if !persisted {
		t.Fatal("tool reply was not physically persisted before exit")
	}
}
