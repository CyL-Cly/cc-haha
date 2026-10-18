package sessioncontroller

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Only /btw's empty-argument onDone branch can append these two system records
// while an ordinary ASK is running. It never changes that ASK's pending input.
func (a *SerialASK) AppendTerminalBtwUsage(scope *cooperative.Scope, records []protocol.JSONObject) error {
	if a == nil || scope == nil || !scope.BelongsTo(a.owner) || a.pending == nil || !a.controller.active || !a.controller.options.NativeTools || !a.controller.options.PublicPrint || !a.controller.options.AsyncPersistence || a.controller.options.WriterOptions.AgentID != "" || len(records) != 2 {
		return cooperative.ErrRestricted
	}
	for _, record := range records {
		if record.String("type") != "system" || record.String("subtype") != "local_command" {
			return cooperative.ErrRestricted
		}
	}
	if !strings.HasPrefix(records[0].String("content"), "<command-name>/btw</command-name>\n") || records[1].String("content") != "<local-command-stdout>Usage: /btw <your question></local-command-stdout>" && records[1].String("content") != "<local-command-stdout>Usage: /btw &lt;your question&gt;</local-command-stdout>" {
		return cooperative.ErrRestricted
	}
	for _, record := range records {
		copy := record.Clone()
		a.controller.messages = append(a.controller.messages, &copy)
	}
	return a.PersistCompactHistory(scope)
}
