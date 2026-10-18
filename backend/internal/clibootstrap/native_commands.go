package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativePluginHooks() ([]protocol.JSONObject, error) {
	plugins, err := m.nativeSettings.LoadPlugins(context.Background(), "", false)
	if err != nil {
		return nil, err
	}
	hooks := []protocol.JSONObject{}
	for _, plugin := range plugins.Enabled {
		if plugin.HooksConfig != nil {
			hooks = append(hooks, plugin.HooksConfig.Clone())
		}
	}
	return hooks, nil
}

func (m *MainPrint) loadNativeCommandDefinitions(ctx context.Context, cwd string, scopes ...*cooperative.Scope) (nativeCommandCatalogValue, error) {
	remote, err := m.nativeRemoteCatalogEnabled(ctx, scopes...)
	if err != nil {
		return nativeCommandCatalogValue{}, err
	}
	if remote {
		// main.tsx's remote-safe command set contains only shipped local
		// commands. Disk/plugin/bundled prompt definitions cannot enter this
		// catalog or execute on this client, including after MCP discovery.
		return nativeCommandCatalogValue{definitions: []skillcommands.Definition{}, plugins: []json.RawMessage{}, remote: true}, nil
	}
	var definitions []skillcommands.Definition
	var metadata []json.RawMessage
	finish := func(scope *cooperative.Scope, local []skillcommands.Definition, plugins pluginregistry.Result) error {
		definitions = local
		bundled, err := m.nativeBundledSkillCatalog()
		if err != nil {
			return err
		}
		if definition, e := m.nativeComputerUseSkillDefinition(); e != nil {
			return e
		} else {
			bundled = append(bundled, definition)
		}
		definitions = append(bundled, definitions...)
		builtins, err := m.nativeBuiltinPromptCommands()
		if err != nil {
			return err
		}
		definitions = append(definitions, builtins...)
		metadata = []json.RawMessage{}
		for _, plugin := range plugins.Enabled {
			value, err := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(plugin.Name)}, protocol.JSONField{Name: "path", Value: quoteMain(plugin.Path)}, protocol.JSONField{Name: "source", Value: quoteMain(plugin.Source)}).MarshalJSON()
			if err != nil {
				return err
			}
			metadata = append(metadata, value)
		}
		return nil
	}
	if len(scopes) == 1 {
		local, plugins, _, failure := m.nativeCommandSources(ctx, scopes[0], cwd)
		if failure != nil {
			err = failure
		} else {
			err = finish(scopes[0], local, plugins)
		}
	} else if len(scopes) == 0 {
		pending, failure := ownerasync.NewOwnerFuture[nativeCommandCatalogValue](m.Owner)
		if failure != nil {
			return nativeCommandCatalogValue{}, failure
		}
		receipt, failure := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			load, failure := m.beginNativeCommandSources(ctx, scope, cwd)
			if failure != nil {
				return pending.Reject(scope, failure)
			}
			// This is the original source join's await continuation. Return
			// from this Read prefix now, so a real ASK can enter while IO waits.
			_, failure = load.done.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
				if failure != nil {
					return pending.Reject(next, failure)
				}
				local, plugins, failure := load.collect(next)
				if failure == nil {
					failure = finish(next, local, plugins)
				}
				if failure != nil {
					return pending.Reject(next, failure)
				}
				return pending.Resolve(next, nativeCommandCatalogValue{definitions: definitions, plugins: metadata})
			})
			return failure
		})
		if failure != nil {
			return nativeCommandCatalogValue{}, failure
		}
		if failure = receipt.Wait(); failure != nil {
			return nativeCommandCatalogValue{}, failure
		}
		return pending.Wait(ctx)
	} else {
		err = cooperative.ErrRestricted
	}
	if err != nil {
		return nativeCommandCatalogValue{}, err
	}
	return nativeCommandCatalogValue{definitions: definitions, plugins: metadata}, nil
}
