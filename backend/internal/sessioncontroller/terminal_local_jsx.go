package sessioncontroller

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (a *SerialASK) AppendTerminalLocalJSX(scope *cooperative.Scope, name string, records []protocol.JSONObject) error {
	if a == nil || scope == nil || !scope.BelongsTo(a.owner) || a.running || a.pending == nil || !a.controller.active || !a.controller.options.NativeTools || !a.controller.options.PublicPrint || !a.controller.options.AsyncPersistence || a.controller.options.WriterOptions.AgentID != "" || name != "mobile" && name != "plan" && name != "buddy" && name != "branch" && name != "resume" && name != "diff" && name != "help" || len(records) < 2 || len(records) > 3 {
		return cooperative.ErrRestricted
	}
	index := 0
	if len(records) == 3 {
		envelope, _ := records[0].Object("message")
		if records[0].String("type") != "user" || !strings.HasPrefix(envelope.String("content"), "<local-command-caveat>") {
			return cooperative.ErrRestricted
		}
		index = 1
	}
	first, second := records[index], records[index+1]
	content := func(value protocol.JSONObject) string {
		if value.String("type") == "system" {
			return value.String("content")
		}
		envelope, _ := value.Object("message")
		return envelope.String("content")
	}
	if !strings.HasPrefix(content(first), "<command-name>/"+name+"</command-name>\n") || !strings.HasPrefix(content(second), "<local-command-stdout>") || !strings.HasSuffix(content(second), "</local-command-stdout>") || first.String("type") != second.String("type") {
		return cooperative.ErrRestricted
	}
	if first.String("type") == "system" {
		if len(records) != 2 || first.String("subtype") != "local_command" || second.String("subtype") != "local_command" {
			return cooperative.ErrRestricted
		}
	} else if first.String("type") != "user" {
		return cooperative.ErrRestricted
	}
	for _, record := range records {
		copy := record.Clone()
		a.controller.messages = append(a.controller.messages, &copy)
	}
	return a.PersistCompactHistory(scope)
}
