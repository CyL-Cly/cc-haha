package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowParallelStructuredChildrenUseSharedSchemaActorAndIndependentOwners(t *testing.T) {
	var requests, firsts atomic.Int32
	both := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []protocol.JSONObject
			Tools    []protocol.JSONObject
		}
		if e := json.Unmarshal(body, &request); e != nil {
			t.Error(e)
		}
		found := false
		for _, tool := range request.Tools {
			if tool.String("name") == "StructuredOutput" {
				found = true
				schema, _ := tool.Object("input_schema")
				if string(schema.Get("required")) != `["answer"]` {
					t.Errorf("actual shared schema lost %s", rawMain(tool))
				}
			}
		}
		if !found {
			t.Error("actual structured child catalogue missing")
		}
		resumed := false
		for _, m := range request.Messages {
			for _, b := range taskBlocks(m.Get("content")) {
				if b.String("type") == "tool_result" {
					resumed = true
					if string(b.Get("is_error")) == "true" {
						t.Errorf("actual concurrent StructuredOutput failed %s", rawMain(b))
					}
				}
			}
		}
		n := requests.Add(1)
		if !resumed {
			if firsts.Add(1) == 2 {
				close(both)
			}
			// The physical model primitive cannot respond until both actual workers
			// reached it, making serial/fake worker substitution fail this assertion.
			select {
			case <-both:
			case <-r.Context().Done():
				return
			}
			emitNativeTaskTool(w, "structured-concurrent-"+string(rune('a'+n)), "StructuredOutput", map[string]any{"answer": "shared actual schema"})
		} else {
			emitPlanFixture(w, int(n), "", "ignored structured child prose")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`const schema={type:'object',properties:{answer:{type:'string'}},required:['answer']};return await parallel([()=>agent('first actual worker',{schema}),()=>agent('second actual worker',{schema})])`)))
	if e != nil {
		t.Fatal(e)
	}
	out, e := workflow.Execute(ctx, workflow.ExecutionOptions{Program: program, RunID: "wf_parallel-abc", WorkflowName: "parallel-schema", Model: "fixture-model", Now: m.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, RunAgent: m.runNativeWorkflowAgent, Concurrency: 2, Emit: func(protocol.JSONObject) error { return nil }})
	if e != nil || out.Error != "" || requests.Load() != 4 || firsts.Load() != 2 || string(out.Result) != `[{"answer":"shared actual schema"},{"answer":"shared actual schema"}]` {
		t.Fatalf("actual parallel schema actor failed %+v %v requests%d firsts%d", out, e, requests.Load(), firsts.Load())
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}
