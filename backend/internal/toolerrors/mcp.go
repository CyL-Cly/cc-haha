package toolerrors

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// MCPErrorJSON follows entrypoints/mcp's getErrorParts → join → trim. The ASK
// formatError truncation is deliberately absent from this raw MCP boundary.
func MCPErrorJSON(err error) json.RawMessage {
	parts := []string{err.Error()}
	var shell *ShellError
	if errors.As(err, &shell) {
		parts = []string{"Exit code " + strconv.Itoa(shell.Code)}
		if shell.Interrupted {
			parts = append(parts, "[Request interrupted by user for tool use]")
		}
		if shell.Stderr != "" {
			parts = append(parts, shell.Stderr)
		}
		if shell.Stdout != "" {
			parts = append(parts, shell.Stdout)
		}
	}
	text := strings.TrimFunc(strings.Join(parts, "\n"), protocol.IsJavaScriptWhitespace)
	if text == "" {
		text = "Error"
	}
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text)))
}
