package automode

import (
	"encoding/json"
	"testing"
)

func TestClassifierResponseRequiresActualSchemaAndKeepsUTF16(t *testing.T) {
	for _, row := range []struct {
		raw, reason        string
		block, unavailable bool
	}{
		{`{"content":[{"type":"tool_use","name":"other","input":{}}]}`, "Classifier returned no tool use block - blocking for safety", true, false},
		{`{"content":[{"type":"tool_use","name":"classify_result","input":{"thinking":"reasoning","shouldBlock":false,"reason":"raw\ud800"}}]}`, "raw�", false, false},
		{`{"content":[{"type":"tool_use","name":"classify_result","input":{"thinking":2,"shouldBlock":false,"reason":"bad"}}]}`, "Invalid classifier response - blocking for safety", true, false},
		{`{"content":null}`, "Classifier unavailable - blocking for safety", true, true},
	} {
		got := ParseClassification(json.RawMessage(row.raw))
		if got.ShouldBlock != row.block || got.Unavailable != row.unavailable || got.Reason != row.reason {
			t.Fatalf("original %s -> %+v", row.raw, got)
		}
		if !row.block && string(got.ReasonJSON) != `"raw\ud800"` {
			t.Fatal("UTF16 replacement leaked into actual decision", string(got.ReasonJSON))
		}
	}
}
