package toolschema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSchemaFeedbackSeparatesHumanContentFromSourceZodReceipt(t *testing.T) {
	d, e := NativeBuiltinDefinition("Read")
	if e != nil {
		t.Fatal(e)
	}
	var in protocol.JSONObject
	_ = json.Unmarshal([]byte(`{"offset":false,"extra":1}`), &in)
	_, e = ParseInput(d, in)
	if e == nil {
		t.Fatal("invalid input accepted")
	}
	content, receipt := ValidationFeedback("Read", e)
	want := "Read failed due to the following issues:\nThe required parameter `file_path` is missing\nAn unexpected parameter `extra` was provided\nThe parameter `offset` type is expected as `number` but provided as `boolean`"
	if content != want || !strings.HasPrefix(receipt, "[\n  {\n") || !strings.Contains(receipt, `"expected": "number"`) {
		t.Fatalf("feedback content=%s receipt=%s", content, receipt)
	}
}

func TestNativeSchemaSafeIntegerIssueOrderAndStripAgentKeys(t *testing.T) {
	d, _ := NativeBuiltinDefinition("Read")
	var in protocol.JSONObject
	_ = json.Unmarshal([]byte(`{"file_path":"x","offset":-9007199254740992}`), &in)
	_, e := ParseInput(d, in)
	if e == nil {
		t.Fatal("unsafe number accepted")
	}
	_, receipt := ValidationFeedback("Read", e)
	if !strings.Contains(receipt, "Integers must be within the safe integer range.") || strings.Index(receipt, `"origin": "int"`) > strings.Index(receipt, `"origin": "number"`) {
		t.Fatal(receipt)
	}
	d, _ = NativeBuiltinDefinition("Agent")
	_ = json.Unmarshal([]byte(`{"description":"test","prompt":"task","unknown":true}`), &in)
	out, e := ParseInput(d, in)
	if e != nil || out.Get("unknown") != nil {
		t.Fatalf("Source Agent strip parser %s %v", raw(out), e)
	}
}
