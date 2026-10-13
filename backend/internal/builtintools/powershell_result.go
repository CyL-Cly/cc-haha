package builtintools

import (
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source's result interpretation intentionally uses its shallow LAST ;/|
// segment heuristic. This is not permission analysis or security validation.
func powerShellInterpretation(command string, code int) (bool, string) {
	last := command
	for _, part := range strings.FieldsFunc(command, func(r rune) bool { return r == ';' || r == '|' }) {
		if protocol.TrimJavaScriptString(part) != "" {
			last = part
		}
	}
	last = protocol.TrimJavaScriptString(last)
	if len(last) > 1 && (last[0] == '&' || last[0] == '.') && protocol.IsJavaScriptWhitespace(rune(last[1])) {
		last = protocol.TrimJavaScriptString(last[1:])
	}
	words := strings.FieldsFunc(last, protocol.IsJavaScriptWhitespace)
	base := ""
	if len(words) > 0 {
		base = strings.TrimPrefix(strings.TrimPrefix(words[0], `"`), "'")
		base = strings.TrimSuffix(strings.TrimSuffix(base, `"`), "'")
		parts := strings.FieldsFunc(base, func(r rune) bool { return r == '\\' || r == '/' })
		if len(parts) > 0 {
			base = parts[len(parts)-1]
		}
		base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	}
	switch base {
	case "grep", "rg", "findstr":
		message := ""
		if code == 1 {
			message = "No matches found"
		}
		return code >= 2, message
	case "robocopy":
		message := ""
		if code == 0 {
			message = "No files copied (already in sync)"
		} else if code >= 1 && code < 8 {
			if code&1 != 0 {
				message = "Files copied successfully"
			} else {
				message = "Robocopy completed (no errors)"
			}
		}
		return code >= 8, message
	default:
		if code != 0 {
			return true, fmt.Sprintf("Command failed with exit code %d", code)
		}
		return false, ""
	}
}

func (s *Service) shellResultIsError(command string, code int) (bool, error) {
	if s.isPowerShell() {
		failed, _ := powerShellInterpretation(command, code)
		return failed, nil
	}
	return bashResultIsError(s.options.Platform, command, code)
}

func (s *Service) shellCompletedData(command string, result processResult) protocol.JSONObject {
	if !s.isPowerShell() {
		return protocol.NewJSONObject(field("stdout", result.Out), field("stderr", result.Err), field("interrupted", result.Interrupted), field("isImage", false), field("noOutputExpected", nativeBashSilent(command, s.options.Platform)))
	}
	data := protocol.NewJSONObject(field("stdout", result.Out), field("stderr", result.Err), field("interrupted", result.Interrupted), field("isImage", false))
	_, meaning := powerShellInterpretation(command, result.Code)
	if meaning != "" {
		data.Set("returnCodeInterpretation", quote(meaning))
	}
	return data
}

func (s *Service) shellBackgroundData(command, id string) protocol.JSONObject {
	data := protocol.NewJSONObject(field("stdout", ""), field("stderr", ""), field("interrupted", false), field("backgroundTaskId", id))
	if !s.isPowerShell() {
		data.Set("isImage", raw(false))
		data.Set("noOutputExpected", raw(nativeBashSilent(command, s.options.Platform)))
	}
	return data
}
