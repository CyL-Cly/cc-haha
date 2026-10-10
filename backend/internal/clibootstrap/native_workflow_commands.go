package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativeWorkflowCommandDefinitions(ctx context.Context, scopes ...*cooperative.Scope) ([]skillcommands.Definition, error) {
	definitions := []skillcommands.Definition{}
	if m.workflowDefinitions == nil {
		return nil, cooperative.ErrRestricted
	}
	load := func(scope *cooperative.Scope) error {
		commands, e := m.workflowDefinitions.CommandsOnOwner(scope)
		if e != nil {
			return e
		}
		for _, command := range commands {
			command := command
			metadata, e := command.Descriptor(scope)
			if e != nil {
				return e
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
				return e
			}
			definitions = append(definitions, definition)
		}
		return nil
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
