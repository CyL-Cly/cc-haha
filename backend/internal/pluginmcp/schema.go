package pluginmcp

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// All plugin and ordinary configuration scopes share the original declaration.
func validateServer(value json.RawMessage) (protocol.JSONObject, bool) {
	parsed, issues := mcpconfig.ValidateServer(value)
	return parsed, len(issues) == 0
}
