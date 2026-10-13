package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) TerminalMessageSelector(ctx context.Context) (tui.MessageSelectorSnapshot, error) {
	result := tui.MessageSelectorSnapshot{}
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		if m.fileHistory != nil {
			var e error
			result.FileHistoryEnabled, e = m.fileHistory.Enabled(scope)
			if e != nil {
				return e
			}
		}
		records, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		for _, message := range records {
			raw, e := message.MarshalJSON()
			if e != nil {
				return e
			}
			var record protocol.JSONObject
			if e = record.UnmarshalJSON(raw); e != nil {
				return e
			}
			text, allowed := tui.SelectableMessageText(record)
			if !allowed {
				continue
			}
			choice := tui.MessageChoice{ID: record.String("uuid"), Text: text}
			if m.fileHistory != nil {
				changed, e := m.fileHistory.ChangedFiles(ctx, scope, choice.ID)
				if e != nil {
					return e
				}
				choice.CanRestoreCode = len(changed) > 0
			}
			result.Choices = append(result.Choices, choice)
		}
		return nil
	})
	return result, e
}
func (m *MainPrint) TerminalSelectMessage(ctx context.Context, selected tui.MessageSelection) (tui.MessageSelectionResult, error) {
	result := tui.MessageSelectionResult{}
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		history, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		records := []protocol.JSONObject{}
		index := -1
		var message protocol.JSONObject
		for i, item := range history {
			data, e := item.MarshalJSON()
			if e != nil {
				return e
			}
			var record protocol.JSONObject
			if e = record.UnmarshalJSON(data); e != nil {
				return e
			}
			records = append(records, record)
			if record.String("uuid") == selected.ID {
				index = i
				message = record
			}
		}
		if index < 0 {
			return errors.New("That message is no longer in the active context (snipped or pre-compact). Choose a more recent message.")
		}
		_, allowed := tui.SelectableMessageText(message)
		if !allowed {
			return errors.New("Selected message is not user-authored")
		}
		text, mode, pastes := tui.MessageResubmit(message)
		var operationError error
		if selected.Action == "summarize" || selected.Action == "summarize_up_to" {
			direction := "from"
			if selected.Action == "summarize_up_to" {
				direction = "up_to"
			}
			if e = m.Host.CompactHistoryOnOwner(ctx, scope, index, direction, selected.Feedback); e != nil {
				return e
			}
			if direction == "from" {
				result.Prefill = text
				result.Mode = mode
				result.PastedContents = pastes
			}
			result.Notice = "Conversation summarized (ctrl+o for history)"
		} else {
			if selected.Action != "both" && selected.Action != "code" && selected.Action != "conversation" {
				return errors.New("Unknown message restore action")
			}
			if selected.Action == "both" || selected.Action == "code" {
				if m.fileHistory == nil {
					return errors.New("File history is disabled")
				}
				if e = m.fileHistory.Rewind(ctx, scope, selected.ID); e != nil {
					operationError = e
				}
			}
			if selected.Action == "both" || selected.Action == "conversation" {
				if e = m.Host.RestoreConversationOnOwner(scope, history[:index]); e != nil {
					operationError = errors.Join(operationError, e)
				} else {
					result.Prefill = text
					result.Mode = mode
					result.PastedContents = pastes
					if restoredMode := message.String("permissionMode"); restoredMode != "" {
						if e := m.Host.SetNativePermissionMode(scope, restoredMode); e != nil {
							operationError = errors.Join(operationError, e)
						} else {
							result.PermissionMode = restoredMode
						}
					}
				}
			}
			result.Notice = "Conversation restored"
			if selected.Action == "code" {
				result.Notice = "Code restored"
			}
		}
		for _, item := range m.Host.History() {
			data, e := item.MarshalJSON()
			if e != nil {
				return e
			}
			var record protocol.JSONObject
			if e = record.UnmarshalJSON(data); e != nil {
				return e
			}
			result.History = append(result.History, record)
		}
		if result.History == nil {
			result.History = []protocol.JSONObject{}
		}
		return operationError
	})
	return result, e
}
