package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowDontAskModeDeniesWriteWithoutPromptAndRetainsRead(t *testing.T) {
	var posts, prompts atomic.Int32
	var target, readable string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		switch n := posts.Add(1); n {
		case 1:
			emitNativeTaskTool(w, "workflow-dontask-write", "Write", map[string]any{"file_path": target, "content": "MUST_NOT_WRITE"})
		case 2:
			if !bytes.Contains(body, []byte(messages.DontAskRejectMessage("Write"))) {
				t.Error("actual child dontAsk denial absent from physical request")
			}
			emitNativeTaskTool(w, "workflow-dontask-read", "Read", map[string]any{"file_path": readable})
		case 3:
			if !bytes.Contains(body, []byte("ACTUAL_DONTASK_READ_OK")) {
				t.Error("readonly consumer failed under actual dontAsk context")
			}
			emitPlanFixture(w, int(n), "", "WORKFLOW_DONTASK_OK")
		default:
			t.Error("unexpected model POST", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		target = filepath.Join(n.CWD, "dontask-denied.txt")
		readable = filepath.Join(n.CWD, "dontask-readable.txt")
		if err := os.WriteFile(readable, []byte("ACTUAL_DONTASK_READ_OK"), 0600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(n.CWD, ".claude", "agents")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "no-asks.md"), []byte("---\nname: no-asks\ndescription: Actual workflow no asks\npermissionMode: dontAsk\ntools: Read, Write\n---\nRun actual private task."), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write"}
		o.ResolvePermission = func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			prompts.Add(1)
			return queryloop.ToolPermission{}, fmt.Errorf("dontAsk must not ask")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	program, err := workflow.CompileBody(utf16.Encode([]rune(`return await agent('Run real deny/read task',{agentType:'no-asks'})`)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_noasks123", WorkflowName: "private-noasks", Model: "fixture-model", Now: m.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: m.runNativeWorkflowAgent, Emit: func(protocol.JSONObject) error { return nil }})
	if err != nil || result.Error != "" || string(result.Result) != `"WORKFLOW_DONTASK_OK"` || posts.Load() != 3 || prompts.Load() != 0 {
		t.Fatal("actual workflow dontAsk", err, result.Error, string(result.Result), posts.Load(), prompts.Load())
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("denied Write changed physical file", err)
	}
}
