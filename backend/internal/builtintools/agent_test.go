package builtintools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func agentMessage(content, usage string) queryloop.Message {
	return queryloop.Message{Record: input(`{"type":"assistant","message":{"role":"assistant","content":` + content + `,"usage":` + usage + `}}`)}
}

func TestAgentNativeBindingInheritsPermissionAndFiltersRecursiveTool(t *testing.T) {
	s, owner, _, permissions := fixture(t)
	permissions.Set("mode", quote("plan"))
	called := false
	s.options.RunAgent = func(_ context.Context, scope *cooperative.Scope, request AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error) {
		called = true
		if request.AgentType != "general-purpose" || request.Prompt != "read the workspace" || request.PermissionContext.String("mode") != "plan" || string(request.ToolUseID) != `"agent-call"` {
			t.Fatalf("actual child request %+v", request)
		}
		if !reflect.DeepEqual(request.Tools, []string{"Bash", "Glob", "Grep", "Read", "Edit", "Write", "NotebookEdit"}) {
			return nil, fmt.Errorf("recursive/unavailable tool pool %v", request.Tools)
		}
		if !reflect.DeepEqual(request.SystemPromptSections, []json.RawMessage{quote(generalPurposeSystemPrompt), quote(agentNotes)}) {
			t.Fatalf("Agent system prompt block boundary changed: %s", request.SystemPromptSections)
		}
		request.PermissionContext.Set("mode", quote("bypassPermissions"))
		future, e := ownerasync.NewOwnerFuture[AgentRunResult](owner)
		if e != nil {
			return nil, e
		}
		e = future.Resolve(scope, AgentRunResult{AgentID: "a1234567890abcdef", Messages: []queryloop.Message{agentMessage(`[{"type":"text","text":"actual child report"}]`, `{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":2,"cache_read_input_tokens":1}`)}, DurationMillis: 17})
		return future, e
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		defs, tools, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		if defs[0].Name != "Agent" || tools[0].StartCallJSON == nil || !tools[0].IsConcurrencySafe(input(`{}`)) {
			t.Fatalf("actual Agent registration %v", defs)
		}
		future, e := tools[0].StartCallJSON(context.Background(), input(`{"description":"Read workspace","prompt":"read the workspace"}`), queryloop.JSONToolCallContext{ToolUseID: quote("agent-call"), Opaque: &structWrongCallContext{}})
		if e == nil || future != nil {
			t.Fatal("unowned scope was accepted")
		}
		future, e = tools[0].StartCallJSON(context.Background(), input(`{"description":"Read workspace","prompt":"read the workspace"}`), queryloop.JSONToolCallContext{ToolUseID: quote("agent-call"), Opaque: &builtinwrite.CallContext{Scope: scope}})
		if e != nil {
			return e
		}
		result, e := future.Await(scope)
		if e != nil {
			return e
		}
		mapped, e := tools[0].MapResultJSON(result.Data, quote("agent-call"))
		if e != nil {
			return e
		}
		if !strings.Contains(string(mapped.Get("content")), "actual child report") || !strings.Contains(string(mapped.Get("content")), "total_tokens: 10") {
			t.Fatalf("actual map %s", raw(mapped))
		}
		if permissions.String("mode") != "plan" {
			t.Fatal("child snapshot mutated parent permission")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Agent driver not invoked")
	}
}

type structWrongCallContext struct{}

func TestAgentFinalizeUsesLastUsageAndEarlierTextAfterToolOnlyFinalTurn(t *testing.T) {
	data, e := finalizeAgent(AgentRequest{Prompt: "task", AgentType: "general-purpose"}, AgentRunResult{AgentID: "a-real-task", DurationMillis: 12, Messages: []queryloop.Message{agentMessage(`[{"type":"text","text":"partial findings"},{"type":"tool_use","id":"one","name":"Read","input":{}}]`, `{"input_tokens":100,"output_tokens":200}`), agentMessage(`[{"type":"tool_use","id":"two","name":"Read","input":{}}]`, `{"input_tokens":4,"output_tokens":5}`)}})
	if e != nil {
		t.Fatal(e)
	}
	var result protocol.JSONObject
	_ = json.Unmarshal(data, &result)
	if number(result, "totalTokens", 0) != 9 || number(result, "totalToolUseCount", 0) != 2 || !strings.Contains(string(result.Get("content")), "partial findings") {
		t.Fatalf("Source finalize receipt %s", data)
	}
	if _, e = finalizeAgent(AgentRequest{}, AgentRunResult{AgentID: "a-empty"}); e == nil || e.Error() != "No assistant messages found" {
		t.Fatalf("missing assistant %v", e)
	}
	data, e = finalizeAgent(AgentRequest{AgentType: "general-purpose"}, AgentRunResult{AgentID: "a-empty-text", Messages: []queryloop.Message{agentMessage(`[{"type":"tool_use","id":"one","name":"Read","input":{}}]`, `{"input_tokens":0,"output_tokens":0}`)}})
	if e != nil {
		t.Fatal(e)
	}
	_ = result.UnmarshalJSON(data)
	mapped, e := mapAgent(result, quote("id"))
	if e != nil || !strings.Contains(string(mapped.Get("content")), "Subagent completed but returned no output") {
		t.Fatalf("empty marker %s %v", raw(mapped), e)
	}
}

func TestAgentFinalizeConsumesLiveEnvelopeAndLatestUsageReference(t *testing.T) {
	usage := &streamhistory.Usage{Raw: []byte(`{"input_tokens":1,"output_tokens":0}`)}
	block := &streamhistory.Block{Object: input(`{"type":"text","text":"early report"}`)}
	envelope := &streamhistory.Envelope{Record: input(`{"type":"assistant","uuid":"actual-child"}`), Message: input(`{"role":"assistant","model":"actual-model"}`), Content: []*streamhistory.Block{block}, Usage: usage}
	actual := queryloop.Message{Assistant: envelope}
	// A content-block yield precedes message_delta. Finalization must observe the
	// retained live objects after the upstream producer updates them.
	usage.Raw = []byte(`{"input_tokens":8,"output_tokens":4,"cache_creation_input_tokens":2,"cache_read_input_tokens":1}`)
	block.Object.Set("text", quote("actual completed report"))
	data, e := finalizeAgent(AgentRequest{AgentType: "general-purpose"}, AgentRunResult{AgentID: "actual-child", DurationMillis: 2, Messages: []queryloop.Message{actual}})
	if e != nil {
		t.Fatal(e)
	}
	var result protocol.JSONObject
	if e = result.UnmarshalJSON(data); e != nil {
		t.Fatal(e)
	}
	if number(result, "totalTokens", 0) != 15 || !strings.Contains(string(result.Get("content")), "actual completed report") || !strings.Contains(string(result.Get("usage")), `"output_tokens":4`) {
		t.Fatalf("live child usage/content was snapshotted: %s", data)
	}
}
