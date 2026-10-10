package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is a presentation boundary. Original JSON remains on Entry and never
// becomes a modified tool result, permission input or model request.
func presentationString(value json.RawMessage) (string, bool) {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return "", false
	}
	return string(utf16.Decode(units)), true
}
func presentationObject(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
func contentPresentation(value json.RawMessage) string {
	if text, ok := presentationString(value); ok {
		return text
	}
	var blocks []protocol.JSONObject
	if json.Unmarshal(value, &blocks) == nil {
		parts := []string{}
		for _, block := range blocks {
			switch block.String("type") {
			case "text":
				parts = append(parts, block.String("text"))
			case "image":
				parts = append(parts, "[Image data]")
			case "tool_reference":
				continue
			}
		}
		return strings.Join(parts, "\n")
	}
	object := presentationObject(value)
	if object.IsObject() {
		if object.Get("stdout") != nil || object.Get("stderr") != nil {
			parts := []string{}
			for _, key := range []string{"stdout", "stderr"} {
				if v := object.String(key); v != "" {
					parts = append(parts, v)
				}
			}
			return strings.Join(parts, "\n")
		}
		if object.Get("content") != nil {
			return contentPresentation(object.Get("content"))
		}
	}
	var pretty strings.Builder
	var buffer []byte
	if json.Valid(value) {
		buffer, _ = json.MarshalIndent(json.RawMessage(value), "", "  ")
		pretty.Write(buffer)
		return pretty.String()
	}
	return string(value)
}
func toolInputPresentation(name string, value json.RawMessage) string {
	object := presentationObject(value)
	for _, key := range []string{"command", "file_path", "notebook_path", "pattern", "query", "url", "description"} {
		if text := object.String(key); text != "" {
			return text
		}
	}
	return contentPresentation(value)
}
func toolResultPresentation(entry Entry, verbose bool) string {
	if entry.IsError {
		return toolErrorPresentation(entry.Output, verbose)
	}
	result := presentationObject(entry.Result)
	if entry.ToolName == "Read" {
		return readResultPresentation(result)
	}
	if !entry.IsError && result.IsObject() {
		switch entry.ToolName {
		case "Bash":
			stdout, stderr := result.String("stdout"), result.String("stderr")
			if stdout == "" && strings.TrimSpace(stderr) == "" {
				if result.String("backgroundTaskId") != "" {
					return "Running in the background (↓ to manage)"
				}
				if text := result.String("returnCodeInterpretation"); text != "" {
					return text
				}
				if string(result.Get("noOutputExpected")) == "true" {
					return "Done"
				}
				return "(No output)"
			}
			return strings.TrimRight(strings.Join([]string{stdout, stderr}, "\n"), "\n")
		}
	}
	return contentPresentation(entry.Output)
}

var toolErrorTag = regexp.MustCompile(`(?is)<tool_use_error(?:\s+[^>]*)?>(.*?)</tool_use_error>`)
var toolErrorOpening = regexp.MustCompile(`(?i)<tool_use_error(?:\s+[^>]*?)?>`)
var toolErrorClosing = regexp.MustCompile(`(?i)</tool_use_error>`)
var sandboxViolationTag = regexp.MustCompile(`<sandbox_violations>[\s\S]*?</sandbox_violations>`)

// The normal error row follows FallbackToolUseErrorMessage's ten-line limit.
// Transcript rendering derives from retained output, not the condensed row.
func toolErrorPresentation(output json.RawMessage, verbose bool) string {
	text, isString := presentationString(output)
	if !isString {
		text = "Tool execution failed"
	} else {
		last := 0
		for _, match := range toolErrorTag.FindAllStringSubmatchIndex(text, -1) {
			before := text[last:match[0]]
			depth := len(toolErrorOpening.FindAllStringIndex(before, -1)) - len(toolErrorClosing.FindAllStringIndex(before, -1))
			if depth == 0 && match[2] != match[3] {
				text = text[match[2]:match[3]]
				break
			}
			last = match[1]
		}
		text = sandboxViolationTag.ReplaceAllString(text, "")
		text = strings.ReplaceAll(strings.ReplaceAll(text, "<error>", ""), "</error>", "")
		text = protocol.TrimJavaScriptString(text)
		if !verbose && strings.Contains(text, "InputValidationError: ") {
			text = "Invalid tool parameters"
		} else if !strings.HasPrefix(text, "Error: ") && !strings.HasPrefix(text, "Cancelled: ") {
			text = "Error: " + text
		}
	}
	lines := strings.Split(text, "\n")
	if !verbose && len(lines) > 10 {
		extra := len(lines) - 10
		label := "lines"
		if extra == 1 {
			label = "line"
		}
		return strings.Join(lines[:10], "\n") + fmt.Sprintf("\n… +%d %s (ctrl+o to see all)", extra, label)
	}
	return text
}
