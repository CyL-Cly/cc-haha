package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowStructuredChildValidationRetryPhysicalToolsAndJournal(t *testing.T) {
	var rounds atomic.Int32
	var path string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Tools    []protocol.JSONObject
			Messages []protocol.JSONObject
		}
		_ = json.Unmarshal(body, &request)
		found := false
		for _, tool := range request.Tools {
			if tool.String("name") == "StructuredOutput" {
				found = true
				schema, _ := tool.Object("input_schema")
				if schema.String("type") != "object" || !strings.Contains(string(rawMain(schema)), "answer") {
					t.Errorf("actual worker lost live schema %s", rawMain(tool))
				}
			}
		}
		if !found || !strings.Contains(string(body), "You MUST return your final answer by calling the StructuredOutput tool exactly once") {
			t.Errorf("not the Source structured worker actual model contract %.1500s", body)
		}
		switch n := rounds.Add(1); n {
		case 1:
			emitNativeTaskTool(w, "schema-invalid", "StructuredOutput", map[string]any{"wrong": false})
		case 2:
			if !strings.Contains(string(body), "Output does not match required schema:") {
				t.Errorf("model could not retry actual Ajv rejection %.1400s", body)
			}
			emitNativeTaskTool(w, "schema-write", "Write", map[string]any{"file_path": path, "content": "actual-structured-workflow"})
		case 3:
			emitNativeTaskTool(w, "schema-read", "Read", map[string]any{"file_path": path})
		case 4:
			if !strings.Contains(string(body), "actual-structured-workflow") {
				t.Error("structured child did not read actual disk state")
			}
			emitNativeTaskTool(w, "schema-final", "StructuredOutput", map[string]any{"answer": "verified actual file"})
		default:
			if !strings.Contains(string(body), "Structured output provided successfully") {
				t.Error("actual StructuredOutput feedback missing")
			}
			if strings.Contains(string(body), `"type":"structured_output"`) {
				t.Error("UI-only structured attachment leaked into provider request")
			}
			emitPlanFixture(w, int(n), "", "this prose must not become the structured result")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	path = filepath.Join(m.options.Native.CWD, "structured-actual.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`const schema={type:'object',properties:{answer:{type:'string'}},required:['answer'],additionalProperties:false};return await agent('Write, Read, then return the schema-conforming result',{schema})`)))
	if e != nil {
		t.Fatal(e)
	}
	journal, e := workflow.NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"), workflow.NativeJournalFiles())
	if e != nil {
		t.Fatal(e)
	}
	options := workflow.ExecutionOptions{Program: program, RunID: "wf_schema123-abc", WorkflowName: "schema-native", Model: "fixture-model", Now: m.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: m.runNativeWorkflowAgent, Journal: journal, Emit: func(protocol.JSONObject) error { return nil }}
	outcome, e := workflow.Execute(ctx, options)
	if e != nil || outcome.Error != "" || rounds.Load() != 5 || string(outcome.Result) != `{"answer":"verified actual file"}` {
		t.Fatalf("real structured worker failed %+v %v POST%d", outcome, e, rounds.Load())
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "actual-structured-workflow" {
		t.Fatal(e, string(data))
	}
	cached, e := journal.Load()
	if e != nil {
		t.Fatal(e)
	}
	options.JournalSnapshot = &cached
	resumed, e := workflow.Execute(ctx, options)
	if e != nil || resumed.Error != "" || string(resumed.Result) != string(outcome.Result) || rounds.Load() != 5 {
		t.Fatalf("real structured journal replay changed %+v %v POST%d", resumed, e, rounds.Load())
	}
}
