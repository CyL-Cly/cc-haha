package toolerrors

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ShellError preserves the original error's fields across tool execution and
// embedded prompt expansion. Output is the merged process stream in Stderr.
type ShellError struct {
	Stdout, Stderr string
	Code           int
	Interrupted    bool
}

func (*ShellError) Error() string { return "Shell command failed" }

func jsSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

// FormatShellError ports formatError/getErrorParts, including UTF-16 slice
// boundaries. It returns JSON so a slice through a surrogate remains exact.
func FormatShellError(e *ShellError) json.RawMessage {
	parts := []string{"Exit code " + strconv.Itoa(e.Code)}
	if e.Interrupted {
		parts = append(parts, "[Request interrupted by user for tool use]")
	}
	if e.Stderr != "" {
		parts = append(parts, e.Stderr)
	}
	if e.Stdout != "" {
		parts = append(parts, e.Stdout)
	}
	return formatErrorParts(parts)
}

// FormatError retains the inner tool executor's plain Error message rather
// than the outer runToolUse wrapper. Shell failures keep their original fields.
func FormatError(e error) json.RawMessage {
	var shell *ShellError
	if errors.As(e, &shell) {
		return FormatShellError(shell)
	}
	return formatErrorParts([]string{e.Error()})
}

func formatErrorParts(parts []string) json.RawMessage {
	text := strings.TrimFunc(strings.Join(parts, "\n"), jsSpace)
	if text == "" {
		text = "Command failed with no output"
	}
	units := utf16.Encode([]rune(text))
	if len(units) <= 10_000 {
		return protocol.QuoteJavaScriptUTF16(units)
	}
	middle := utf16.Encode([]rune("\n\n... [" + strconv.Itoa(len(units)-10_000) + " characters truncated] ...\n\n"))
	result := append(append(append([]uint16(nil), units[:5000]...), middle...), units[len(units)-5000:]...)
	return protocol.QuoteJavaScriptUTF16(result)
}
