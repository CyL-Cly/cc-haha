package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"net/url"
)

// The terminal editor uses the same validated Markdown mutation and catalog
// services as the desktop. Its invalidation refreshes the running registry.
func (m *MainPrint) TerminalAgents(ctx context.Context, method, name string, body json.RawMessage) (protocol.JSONObject, error) {
	n := m.options.Native
	service, err := agentsapi.New(agentsapi.Options{
		HomeDir: n.Home, ConfigDir: n.ConfigHomeDirectory(), WorkDir: n.CWD,
		Load:           func(ctx context.Context, _ string) (*agentcatalog.Result, error) { return m.AgentDefinitions(ctx) },
		AvailableTools: func(ctx context.Context, _ string) ([]string, error) { return m.nativeSettings.CustomAgentTools(ctx) },
		Invalidate:     func(ctx context.Context) error { _, err := m.ReloadComponents(ctx); return err },
		Locked: func(ctx context.Context) (bool, error) {
			var locked bool
			receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				var err error
				locked, err = m.agents.Restricted(scope)
				return err
			})
			if err == nil {
				err = receipt.Wait()
			}
			return locked, err
		},
		UpdateOverride: func(ctx context.Context, name string, values storage.Object) error {
			receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
				patch := clisettings.Patch{Object: protocol.NewJSONObject()}
				entry := protocol.NewJSONObject()
				for _, key := range []string{"model", "effort"} {
					value, exists := values[key]
					if values == nil || exists && (value == nil || string(rawMain(value)) == "null") {
						patch.UndefinedPaths = append(patch.UndefinedPaths, []json.RawMessage{quoteMain("builtInAgentOverrides"), quoteMain(name), quoteMain(key)})
					} else if exists {
						entry.Set(key, rawMain(value))
					}
				}
				patch.Object.Set("builtInAgentOverrides", rawMain(map[string]any{name: entry}))
				return m.writePickerSetting(patch)
			})
			if err == nil {
				err = receipt.Wait()
			}
			return err
		},
		Reload: func(ctx context.Context, id string) (protocol.JSONObject, error) {
			if id != m.Controller.SessionID() {
				return protocol.JSONObject{}, errors.New("Session not found")
			}
			return m.ReloadComponents(ctx)
		},
	})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	request, err := url.Parse("/api/agents")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	parts := []string{"api", "agents"}
	if name != "" {
		parts = append(parts, name)
	}
	if method == "DELETE" {
		var context protocol.JSONObject
		_ = context.UnmarshalJSON(body)
		query := request.Query()
		for _, key := range []string{"scope", "target"} {
			if value := context.String(key); value != "" {
				query.Set(key, value)
			}
		}
		request.RawQuery = query.Encode()
	}
	_, result, err := service.Handle(ctx, method, request, parts, body)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var object protocol.JSONObject
	err = object.UnmarshalJSON(rawMain(result))
	return object, err
}
