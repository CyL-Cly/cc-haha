package sdkhost

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The ordinary leader inbox has already admitted these original user records
// into canonical history. REPL appends them before starting its actual query;
// interactive rendering needs that same observation, without another record.
func (h *Host) observesNativeTeammateInput(prompt *sessioncontroller.ProcessedInput) bool {
	if h == nil || prompt == nil || prompt.NoQuery || prompt.TerminalLocalJSX || h.options.Print == nil {
		return false
	}
	print := h.options.Print
	if !print.NativeProcess || !print.NativeInteractive || print.AgentID != "" || print.TeamWorker != nil {
		return false
	}
	for _, record := range prompt.Messages {
		if record.String("type") != "user" || string(record.Get("isMeta")) == "true" {
			continue
		}
		var content string
		if json.Unmarshal(object(record.Get("message")).Get("content"), &content) == nil && strings.Contains(content, "<teammate-message") {
			return true
		}
	}
	return false
}
