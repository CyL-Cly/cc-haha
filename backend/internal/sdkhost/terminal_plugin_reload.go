package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// PrepareTerminalPluginReloadOnOwner is the native interactive local command,
// before its actual refresh. It cannot admit headless or running ASK callers.
func (h *Host) PrepareTerminalPluginReloadOnOwner(scope *cooperative.Scope, args string) (protocol.JSONObject, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote("<command-name>/reload-plugins</command-name>\n            <command-message>reload-plugins</command-message>\n            <command-args>" + args + "</command-args>")}))
	_ = h.options.Factory.CreateLocalCaveat()
	return user, scope.AwaitFulfilled()
}

func (h *Host) AppendTerminalPluginReloadOnOwner(scope *cooperative.Scope, user protocol.JSONObject, output string) error {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing || user.String("type") != "user" {
		return cooperative.ErrRestricted
	}
	if err := scope.AwaitFulfilled(); err != nil {
		return err
	}
	system := h.options.Factory.CreateLocalCommandInput("<local-command-stdout>" + output + "</local-command-stdout>")
	if err := scope.AwaitFulfilled(); err != nil {
		return err
	}
	caveat := h.options.Factory.CreateLocalCaveat()
	return h.serial.AppendTerminalPluginReload(scope, []protocol.JSONObject{caveat, user, system})
}
