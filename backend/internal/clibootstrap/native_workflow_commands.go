package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func (m *MainPrint) nativeWorkflowCommandDefinitions(ctx context.Context, scopes ...*cooperative.Scope) ([]skillcommands.Definition, error) {
	return m.nativeWorkflowCommandDefinitionsAt(ctx, m.options.Native.CWD, scopes...)
}

func (m *MainPrint) nativeWorkflowCommandDefinitionsAt(ctx context.Context, cwd string, scopes ...*cooperative.Scope) ([]skillcommands.Definition, error) {
	definitions := []skillcommands.Definition{}
	if m.workflowDefinitions == nil {
		return nil, cooperative.ErrRestricted
	}
	load := func(scope *cooperative.Scope) error {
		commands, e := m.workflowDefinitions.CommandsForDirectoryOnOwner(scope, cwd)
		if e != nil {
			return e
		}
		definitions, e = m.nativeWorkflowCommandDefinitionsFromCommands(scope, commands)
		return e
	}
	var e error
	if len(scopes) == 1 {
		e = load(scopes[0])
	} else if len(scopes) == 0 {
		e = m.Owner.Run(ctx, load)
	} else {
		return nil, cooperative.ErrRestricted
	}
	return definitions, e
}

// Shared pure Owner projection for the retained workflow source. Starting the
// original future and formatting its completed commands remain separate steps.
func (m *MainPrint) nativeWorkflowCommandDefinitionsFromCommands(scope *cooperative.Scope, commands []*workflowcommands.Command) ([]skillcommands.Definition, error) {
	definitions := []skillcommands.Definition{}
	for _, command := range commands {
		command := command
		metadata, e := command.Descriptor(scope)
		if e != nil {
			return nil, e
		}
		definition, e := skillcommands.NewNativeCommand(metadata, func(ctx context.Context, args *string, inv skillcommands.Invocation) (string, error) {
			if inv.NativeScope == nil || !inv.NativeScope.BelongsTo(m.Owner) {
				return "", cooperative.ErrRestricted
			}
			value := ""
			if args != nil {
				value = *args
			}
			future, e := command.BeginPrompt(inv.NativeScope, quoteMain(value))
			if e != nil {
				return "", e
			}
			raw, e := future.Await(inv.NativeScope)
			if e != nil {
				return "", e
			}
			var blocks []protocol.JSONObject
			if e = json.Unmarshal(raw, &blocks); e != nil {
				return "", e
			}
			if len(blocks) != 1 || blocks[0].String("type") != "text" {
				return "", errors.New("actual Workflow command prompt blocks")
			}
			return blocks[0].String("text"), nil
		})
		if e != nil {
			return nil, e
		}
		definition = definition.WithNativeEnablement(command.IsEnabled)
		definitions = append(definitions, definition)
	}
	return definitions, nil
}
