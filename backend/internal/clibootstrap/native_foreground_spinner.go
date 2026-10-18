package clibootstrap

import (
	"encoding/json"
	"errors"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// AgentTool counts completed assistant blocks because runAgent filters child
// streaming events. This is the existing foreground child's parent ref channel;
// it never publishes a child stream or commits the parent's render state.
func (state *nativeForegroundAgent) observeCompletedAssistantSpinner(scope *cooperative.Scope, raw json.RawMessage) error {
	if state == nil || state.parent == nil || state.child == nil || !scope.BelongsTo(state.parent.Owner) || state.child.Owner == state.parent.Owner {
		return cooperative.ErrRestricted
	}
	m := state.parent
	detached := state.child.options.DetachedAgent
	if detached == nil || detached.authParent != m || !state.child.agentChild {
		return cooperative.ErrRestricted
	}
	if state.promoted || state.backgrounded.Load() || !m.options.Interactive || m.options.OnTerminalSpinner == nil {
		return nil
	}
	if !m.terminalFullscreenBound(scope) {
		return cooperative.ErrRestricted
	}
	length, err := foregroundCompletedAssistantLength(raw)
	if err != nil || length == 0 {
		return err
	}
	return m.options.OnTerminalSpinner(tui.TerminalSpinnerProgress{AddedResponseUnits: length})
}

func foregroundCompletedAssistantLength(raw json.RawMessage) (int, error) {
	var envelope protocol.JSONObject
	if err := envelope.UnmarshalJSON(raw); err != nil {
		return 0, err
	}
	if envelope.String("type") != "assistant" {
		return 0, nil
	}
	message, ok := envelope.Object("message")
	if !ok {
		return 0, errors.New("completed assistant message missing content")
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
		return 0, err
	}
	length := 0
	for _, block := range blocks {
		field := ""
		switch block.String("type") {
		case "text":
			field = "text"
		case "thinking":
			field = "thinking"
		case "redacted_thinking":
			field = "data"
		case "tool_use":
			input, err := protocol.ParseAndStringifyJavaScriptJSON(block.Get("input"))
			if err != nil {
				return 0, err
			}
			length += len(utf16.Encode([]rune(string(input))))
			continue
		default:
			continue
		}
		units, err := protocol.DecodeJavaScriptJSONString(block.Get(field))
		if err != nil {
			return 0, err
		}
		length += len(units)
	}
	return length, nil
}
