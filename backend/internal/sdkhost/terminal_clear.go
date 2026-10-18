package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (h *Host) PrepareTerminalClearLocalInputOnOwner(scope *cooperative.Scope, args string) (protocol.JSONObject, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	// Original getCommand resolves all three aliases to command.name="clear".
	// processSlashCommand allocates the user and an unused caveat before load.
	text := "<command-name>/clear</command-name>\n            <command-message>clear</command-message>\n            <command-args>" + args + "</command-args>"
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(text)}))
	_ = h.options.Factory.CreateLocalCaveat()
	return user, scope.AwaitFulfilled()
}

func (h *Host) AppendTerminalClearLocalReplayOnOwner(scope *cooperative.Scope, user protocol.JSONObject) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing || user.String("type") != "user" {
		return cooperative.ErrRestricted
	}
	if err := scope.AwaitFulfilled(); err != nil {
		return err
	}
	system := h.options.Factory.CreateLocalCommandInput("<local-command-stdout></local-command-stdout>")
	if err := scope.AwaitFulfilled(); err != nil {
		return err
	}
	caveat := h.options.Factory.CreateLocalCaveat()
	history := append(h.serial.History(), queryloop.Message{Record: caveat}, queryloop.Message{Record: user}, queryloop.Message{Record: system})
	if err := h.serial.ReplaceCompactHistory(scope, history); err != nil {
		return err
	}
	// Use the existing same-Owner Record/Writer path. A local command creates
	// history for the next ASK without opening a model request of its own.
	return h.serial.PersistCompactHistory(scope)
}

// ClearTerminalHistoryOnOwner clears the same SerialASK and live-envelope map.
// It is an idle REPL transition, never startup activation or a replacement Host.
func (h *Host) ClearTerminalHistoryOnOwner(scope *cooperative.Scope) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return cooperative.ErrRestricted
	}
	if err := h.RestoreConversationOnOwner(scope, nil); err != nil {
		return err
	}
	h.nativeCompactFailures = 0
	h.sideQuestionCandidate, h.sideQuestionCache = nil, nil
	h.toolSearchCatalogue = nil
	return nil
}

func (h *Host) InstallTerminalClearHookMessages(scope *cooperative.Scope, records []queryloop.Message) error {
	if h == nil || h.askRunning || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeInteractive {
		return cooperative.ErrRestricted
	}
	return h.serial.ReplaceCompactHistory(scope, records)
}
