package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type skillValidationMarkerFixture struct{}

func (skillValidationMarkerFixture) Error() string { return "ordinary display validation" }
func (skillValidationMarkerFixture) SkillValidationMessageJSON() json.RawMessage {
	return json.RawMessage(`"raw \ud800 identity"`)
}

func TestSkillValidationJSONMarkerCannotChangeOtherToolOrPhase(t *testing.T) {
	for _, row := range []struct {
		name, tool, phase string
		marker            error
		receipt, content  string
	}{
		{"non-Skill validation marker", "Write", "validation", skillValidationMarkerFixture{}, "Error: ordinary display validation", "<tool_use_error>ordinary display validation</tool_use_error>"},
		{"ordinary Skill validation", "Skill", "validation", errors.New("ordinary validation"), "Error: ordinary validation", "<tool_use_error>ordinary validation</tool_use_error>"},
		{"Skill parse marker", "Skill", "parse", skillValidationMarkerFixture{}, "InputValidationError: ordinary display validation", "<tool_use_error>InputValidationError: ordinary display validation</tool_use_error>"},
		{"Skill call marker", "Skill", "call", skillValidationMarkerFixture{}, "Error: ordinary display validation", "ordinary display validation"},
	} {
		t.Run(row.name, func(t *testing.T) {
			h := newBatchHarness(t)
			h.Tool.Name = row.tool
			h.Uses[0].Block.Object.Set("name", quoted(row.tool))
			switch row.phase {
			case "validation":
				h.Tool.ValidateInput = func(context.Context, protocol.JSONObject, any) error { return row.marker }
			case "parse":
				h.Tool.ParseInput = func(protocol.JSONObject) (protocol.JSONObject, error) { return protocol.JSONObject{}, row.marker }
			case "call":
				h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
					return ToolCallResult{}, row.marker
				}
			}
			batch, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
			if e != nil {
				t.Fatal(e)
			}
			out := []Message{}
			_, e = batch.Execute(context.Background(), Round{}, h.Uses, func(m Message) error { out = append(out, m); return nil })
			if e != nil {
				t.Fatal(e)
			}
			if len(out) != 1 {
				t.Fatal("original failure count", len(out))
			}
			api, _ := out[0].Record.Object("message")
			var blocks []protocol.JSONObject
			if e = json.Unmarshal(api.Get("content"), &blocks); e != nil {
				t.Fatal(e)
			}
			if len(blocks) != 1 || string(blocks[0].Get("is_error")) != "true" || blocks[0].String("content") != row.content || out[0].Record.String("toolUseResult") != row.receipt {
				t.Fatalf("unrelated error branch changed: %s", api.Get("content"))
			}
		})
	}
}
