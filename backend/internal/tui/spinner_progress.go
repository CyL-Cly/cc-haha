package tui

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalSpinnerProgress is an internal observation of the original stream
// callback. It is never persisted, sent as SDK output, or used to admit work.
type TerminalSpinnerProgress struct {
	Mode               string
	AddedResponseUnits int
	Verb               *string
	// QueryEvent preserves the actual interactive callback's commit boundary.
	// A length-only delta changes a ref without re-rendering the parent.
	QueryEvent json.RawMessage
}

// TerminalSpinnerStreamProgress mirrors handleMessageFromStream's spinner
// callbacks. Length is the original JS string length, including JSON input
// and thinking deltas, and excluding cryptographic signature deltas.
func TerminalSpinnerStreamProgress(event json.RawMessage) (TerminalSpinnerProgress, bool, error) {
	var input protocol.JSONObject
	if err := input.UnmarshalJSON(event); err != nil {
		return TerminalSpinnerProgress{}, false, err
	}
	result := TerminalSpinnerProgress{}
	switch input.String("type") {
	case "message_stop":
		result.Mode = "tool-use"
	case "content_block_start":
		block, _ := input.Object("content_block")
		switch block.String("type") {
		case "thinking", "redacted_thinking":
			result.Mode = "thinking"
		case "text":
			result.Mode = "responding"
		case "tool_use", "server_tool_use", "web_search_tool_result", "code_execution_tool_result", "mcp_tool_use", "mcp_tool_result", "container_upload", "web_fetch_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result", "compaction":
			result.Mode = "tool-input"
		default:
			return result, false, nil
		}
	case "content_block_delta":
		delta, _ := input.Object("delta")
		field := ""
		switch delta.String("type") {
		case "text_delta":
			field = "text"
		case "input_json_delta":
			field = "partial_json"
		case "thinking_delta":
			field = "thinking"
		default:
			return result, false, nil
		}
		units, err := protocol.DecodeJavaScriptJSONString(delta.Get(field))
		if err != nil {
			return result, false, err
		}
		result.AddedResponseUnits = len(units)
	case "content_block_stop":
		return result, false, nil
	default:
		result.Mode = "responding"
	}
	return result, true, nil
}
