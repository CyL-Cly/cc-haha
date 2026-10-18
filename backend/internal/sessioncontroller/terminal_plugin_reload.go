package sessioncontroller

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AppendTerminalPluginReload appends the original local command's messages
// without replacing live envelopes or clearing the foreground read cache.
func (a *SerialASK) AppendTerminalPluginReload(scope *cooperative.Scope, records []protocol.JSONObject) error {
	if a == nil || scope == nil || !scope.BelongsTo(a.owner) || a.running || a.pending == nil || !a.controller.active || !a.controller.options.NativeTools || !a.controller.options.PublicPrint || !a.controller.options.AsyncPersistence || a.controller.options.WriterOptions.AgentID != "" || len(records) != 3 {
		return cooperative.ErrRestricted
	}
	caveat, _ := records[0].Object("message")
	user, _ := records[1].Object("message")
	if records[0].String("type") != "user" || !strings.HasPrefix(caveat.String("content"), "<local-command-caveat>") || records[1].String("type") != "user" || !strings.HasPrefix(user.String("content"), "<command-name>/reload-plugins</command-name>") || records[2].String("type") != "system" || records[2].String("subtype") != "local_command" || !strings.HasPrefix(records[2].String("content"), "<local-command-stdout>Reloaded: ") {
		return cooperative.ErrRestricted
	}
	for _, record := range records {
		copy := record.Clone()
		a.controller.messages = append(a.controller.messages, &copy)
	}
	return a.PersistCompactHistory(scope)
}
