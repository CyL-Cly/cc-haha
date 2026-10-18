package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// These are the original handlers' command events, before their actual
// operations. Success-only marketplace events are emitted by the operation.
func (m *MainCommand) nativeCommandPluginEvent(scope *cooperative.Scope, command plugincli.Command) error {
	if !m.ownsNativeCommandFactory() {
		return nil
	}
	if !scope.BelongsTo(m.Owner) || m.commandEvents == nil {
		return cooperative.ErrRestricted
	}
	name := ""
	metadata := protocol.NewJSONObject()
	switch command.Action {
	case "list":
		name = "tengu_plugin_list_command"
	case "install", "uninstall", "enable", "disable", "update":
		name = "tengu_plugin_" + command.Action + "_command"
		if command.Action == "disable" && command.All {
			break
		}
		identifier := strings.Split(command.Argument, "@")
		metadata.Set("_PROTO_plugin_name", quoteMain(identifier[0]))
		if len(identifier) > 1 && identifier[1] != "" {
			metadata.Set("_PROTO_marketplace_name", quoteMain(identifier[1]))
		}
		if command.Action != "update" {
			scopeName := command.Scope
			if scopeName == "" {
				scopeName = "auto"
				if command.Action == "install" || command.Action == "uninstall" || command.Cowork {
					scopeName = "user"
				}
			}
			metadata.Set("scope", quoteMain(scopeName))
		}
	default:
		return nil
	}
	return m.commandEvents.Log(scope, analytics.Event{Name: name, Metadata: metadata})
}

// Only detached scalar metadata from the actual operation crosses the worker
// boundary. The callback awaits event admission, never enrichment/export HTTP.
func (m *MainCommand) emitPluginCLIEvent(ctx context.Context, event analytics.Event) {
	if !m.ownsNativeCommandFactory() || m.commandEvents == nil {
		return
	}
	event.Metadata = event.Metadata.Clone()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error { return m.commandEvents.Log(scope, event) })
	if err == nil {
		_ = receipt.Wait()
	}
}
