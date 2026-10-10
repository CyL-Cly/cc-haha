package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// defaultShell is an optional release enum. Native execution resolves actual
// shell availability separately; reading a saved preference never fabricates an
// executable or changes the finite settings profile.
func nativeTerminalShellErrors(value json.RawMessage, file string) []json.RawMessage {
	if value == nil {
		return nil
	}
	encoded := protocol.StringifyValidatedJavaScriptJSON(value)
	if string(encoded) == `"bash"` || string(encoded) == `"powershell"` {
		return nil
	}
	issue := enumError(file, "defaultShell", []string{"bash", "powershell"})
	var record protocol.JSONObject
	_ = record.UnmarshalJSON(issue)
	record.Delete("docLink")
	issue, _ = record.MarshalJSON()
	return []json.RawMessage{issue}
}
