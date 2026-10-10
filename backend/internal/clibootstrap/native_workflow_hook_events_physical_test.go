package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowCommandHookSDKEventsBorrowParentOwnerAndStreamPhysicalOutput(t *testing.T) {
	var posts atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		posts.Add(1)
		emitPlanFixture(w, 1, "", "PHYSICAL_EVENT_FINAL")
	}))
	defer model.Close()
	var mu sync.Mutex
	events := []protocol.JSONObject{}
	parent := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		script := filepath.Join(n.CWD, "event-hook.py")
		code := "import sys,json,time\nx=json.load(sys.stdin)\nprint('PHYSICAL_STDOUT',flush=True)\nsys.stderr.write('PHYSICAL_STDERR\\n');sys.stderr.flush()\ntime.sleep(1.25)\n"
		if e := os.WriteFile(script, []byte(code), 0600); e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(n.CWD, ".claude", "agents")
		_ = os.MkdirAll(dir, 0700)
		markdown := fmt.Sprintf("---\nname: event-worker\ndescription: Actual SDK event emitter\ntools: Read\nhooks:\n  Stop:\n    - hooks:\n        - type: command\n          command: %s\n---\nReturn the literal result", strconv.Quote("/usr/bin/python3 "+strconv.Quote(script)))
		if e := os.WriteFile(filepath.Join(dir, "event.md"), []byte(markdown), 0600); e != nil {
			t.Fatal(e)
		}
	}, func(o *MainPrintOptions) {
		o.HookEvents = true
		o.Arguments.IncludeHookEvents = true
		o.OnOutput = func(raw []byte) error {
			var message protocol.JSONObject
			if e := message.UnmarshalJSON(raw); e != nil {
				return e
			}
			if message.String("subtype") == "hook_started" || message.String("subtype") == "hook_progress" || message.String("subtype") == "hook_response" {
				mu.Lock()
				events = append(events, message)
				mu.Unlock()
			}
			return nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`return await agent('Return actual SDK hook outcome',{agentType:'event-worker'})`)))
	if e != nil {
		t.Fatal(e)
	}
	result, e := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_events123", WorkflowName: "events", Model: "fixture-model", Now: parent.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: parent.runNativeWorkflowAgent, Emit: func(protocol.JSONObject) error { return nil }})
	if e != nil || result.Error != "" || string(result.Result) != `"PHYSICAL_EVENT_FINAL"` || posts.Load() != 1 {
		t.Fatal("actual event workflow failed", result, e, posts.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("expected actual started/periodic progress/response, got %s", rawMain(events))
	}
	id := events[0].String("hook_id")
	for i, event := range events {
		if event.String("hook_id") != id || event.String("session_id") != parent.Controller.SessionID() || event.String("hook_name") != "SubagentStop" || event.String("hook_event") != "SubagentStop" {
			t.Fatalf("event%d lost actual parent identity %s", i, rawMain(event))
		}
	}
	if events[0].String("subtype") != "hook_started" || events[1].String("subtype") != "hook_progress" || events[2].String("subtype") != "hook_response" || events[1].String("stdout") != "PHYSICAL_STDOUT\n" || events[1].String("stderr") != "PHYSICAL_STDERR\n" || events[2].String("outcome") != "success" || string(events[2].Get("exit_code")) != "0" {
		t.Fatal("SDK events did not carry real process output", string(rawMain(events)))
	}
}
