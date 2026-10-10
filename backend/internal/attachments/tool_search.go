package attachments

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func projectDeferredToolsDelta(value protocol.JSONObject) (json.RawMessage, error) {
	parts := []json.RawMessage{}
	for _, field := range []string{"addedLines", "removedNames"} {
		var lines []json.RawMessage
		if e := json.Unmarshal(value.Get(field), &lines); e != nil || lines == nil {
			return nil, &CapabilityError{Boundary: "malformed deferred-tools delta lines"}
		}
		if len(lines) == 0 {
			continue
		}
		joined, e := protocol.JoinJavaScriptValuesJSON(lines, "\n")
		if e != nil {
			return nil, e
		}
		header := "The following deferred tools are now available via ToolSearch:\n"
		if field == "removedNames" {
			header = "The following deferred tools are no longer available (their MCP server disconnected). Do not search for them — ToolSearch will return no match:\n"
		}
		text, e := concatenate(quoted(header), joined)
		if e != nil {
			return nil, e
		}
		parts = append(parts, text)
	}
	return protocol.JoinJavaScriptValuesJSON(parts, "\n\n")
}
