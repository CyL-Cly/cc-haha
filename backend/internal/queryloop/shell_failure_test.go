package queryloop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func TestShellFailureFeedbackRetainsSourceContentAndReceiptWithoutToolWrapper(t *testing.T) {
	h := newBatchHarness(t)
	h.Tool.Name = "Bash"
	h.Uses[0].Block.Object.Set("name", quoted("Bash"))
	h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
		return ToolCallResult{}, &toolerrors.ShellError{Stderr: "actual merged output\n", Code: 7}
	}
	batch, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if e != nil {
		t.Fatal(e)
	}
	var delivered Message
	if _, e = batch.Execute(context.Background(), Round{ToolContext: "original"}, h.Uses, func(m Message) error { delivered = m; return nil }); e != nil {
		t.Fatal(e)
	}
	var api protocol.JSONObject
	if e = json.Unmarshal(delivered.Record.Get("message"), &api); e != nil {
		t.Fatal(e)
	}
	var blocks []protocol.JSONObject
	if e = json.Unmarshal(api.Get("content"), &blocks); e != nil || len(blocks) != 1 {
		t.Fatalf("error feedback%s %v", api.Get("content"), e)
	}
	if blocks[0].String("content") != "Exit code 7\nactual merged output" || string(blocks[0].Get("is_error")) != "true" || delivered.Record.String("toolUseResult") != "Error: Exit code 7\nactual merged output" || delivered.Record.String("sourceToolAssistantUUID") != "original-assistant" {
		t.Fatalf("actual error feedback%s", delivered.Record.Get("message"))
	}
}
