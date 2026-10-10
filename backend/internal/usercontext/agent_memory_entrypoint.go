package usercontext

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TruncateAgentMemoryEntrypoint shares Source's UTF-16/line index budget with
// ordinary auto-memory. The returned JSON string retains lone slice units.
func TruncateAgentMemoryEntrypoint(content string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(truncateMemoryUnits(content))
}
