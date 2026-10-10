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
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowStopCommandHookBlocksThenContinuesSameHarnessAndClears(t *testing.T) {
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if requests.Add(1) == 1 {
			emitPlanFixture(w, 1, "", "FIRST_FINAL")
			return
		}
		if !strings.Contains(string(body), "Stop hook feedback:\\nACTUAL_STOP_RETRY") {
			t.Errorf("actual feedback missing %.2200s", body)
		}
		emitPlanFixture(w, 2, "", "RETRIED_FINAL")
	}))
	defer model.Close()
	var journal string
	parent := backgroundAgentMainWithSetup(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		journal = filepath.Join(n.CWD, "actual-hook-inputs.jsonl")
		script := filepath.Join(n.CWD, "actual-stop.py")
		code := fmt.Sprintf("import sys,json,os\nx=json.load(sys.stdin)\np=%q\nold=os.path.exists(p)\nwith open(p,'a') as f:f.write(json.dumps(x)+'\\n')\nprint(json.dumps({'continue':False,'stopReason':'Actual hook finished'} if old else {'decision':'block','reason':'ACTUAL_STOP_RETRY'}))\n", journal)
		if e := os.WriteFile(script, []byte(code), 0600); e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(n.CWD, ".claude", "agents")
		_ = os.MkdirAll(dir, 0700)
		markdown := fmt.Sprintf("---\nname: stop-worker\ndescription: Actual Stop lifecycle\ntools: Read\nhooks:\n  Stop:\n    - hooks:\n        - type: command\n          command: %s\n---\nFinish the actual task", strconv.Quote("/usr/bin/python3 "+strconv.Quote(script)))
		if e := os.WriteFile(filepath.Join(dir, "stop.md"), []byte(markdown), 0600); e != nil {
			t.Fatal(e)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`return await agent('Complete actual hook lifecycle',{agentType:'stop-worker'})`)))
	if e != nil {
		t.Fatal(e)
	}
	result, e := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_hooks123", WorkflowName: "hooks", Model: "fixture-model", Now: parent.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: parent.runNativeWorkflowAgent, Emit: func(protocol.JSONObject) error { return nil }})
	if e != nil || result.Error != "" || string(result.Result) != `"RETRIED_FINAL"` || requests.Load() != 2 {
		t.Fatalf("actual Stop hook failed %+v %v requests%d", result, e, requests.Load())
	}
	bytes, e := os.ReadFile(journal)
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(string(bytes)), "\n")
	if len(lines) != 2 {
		t.Fatal("actual hook did not run twice", string(bytes))
	}
	inputs := make([]protocol.JSONObject, 2)
	for i, line := range lines {
		if e = inputs[i].UnmarshalJSON([]byte(line)); e != nil {
			t.Fatal(e)
		}
	}
	if string(inputs[0].Get("stop_hook_active")) != "false" || string(inputs[1].Get("stop_hook_active")) != "true" || inputs[0].String("last_assistant_message") != "FIRST_FINAL" || inputs[1].String("last_assistant_message") != "RETRIED_FINAL" || inputs[0].String("hook_event_name") != "SubagentStop" || inputs[0].String("session_id") != parent.Controller.SessionID() || inputs[0].String("agent_type") != "stop-worker" {
		t.Fatal("actual Source hook input lost state", string(bytes))
	}
	if _, e = os.Stat(inputs[0].String("agent_transcript_path")); e != nil {
		t.Fatal("hook did not receive actual child transcript", e)
	}
	store := parent.hook.NativeCommandStore()
	if store == nil {
		t.Fatal("actual shared hook store missing")
	}
	if count := parent.hook.NativeAgentHookCount(inputs[0].String("agent_id")); count != 0 {
		t.Fatal("actual Agent finally did not clear frontmatter registration", count)
	}
	// A new normal workflow agent shares the same Hook store but has no
	// frontmatter registration. The previous lifecycle must have cleared it.
	if e = parent.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}
