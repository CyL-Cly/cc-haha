package clibootstrap

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) terminalAgentMCPServers(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if m.agents == nil {
		return nil, nil
	}
	agents, err := m.agents.List(scope)
	if err != nil {
		return nil, err
	}
	rows := []protocol.JSONObject{}
	for _, agent := range agents.All {
		rows = append(rows, agent.Fields.Clone())
	}
	return mcpconfig.AgentServers(rows)
}

// Pre-authentication uses only a configured agent's first inline declaration.
// Source does not connect a peer or provide manual-callback input in this view.
func (m *MainPrint) StartTerminalAgentMCPAuthentication(ctx context.Context, name string, onURL func(string)) error {
	if m == nil || m.Owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	ctx, release := m.nativeTerminalMCPAuthLife.bind(ctx)
	defer release()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.mcpBorrowed || m.mcpRuntime == nil || m.mcpConfigurations == nil {
			return cooperative.ErrRestricted
		}
		rows, err := m.terminalAgentMCPServers(scope)
		if err != nil {
			return err
		}
		config := protocol.JSONObject{}
		for _, row := range rows {
			if row.String("name") == name && string(row.Get("needsAuth")) == "true" {
				config = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: row.Get("transport")}, protocol.JSONField{Name: "url", Value: row.Get("url")})
				break
			}
		}
		if !config.IsObject() {
			return errors.New("Configured agent MCP server does not support authentication: " + name)
		}
		pending, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			return struct{}{}, m.mcpRuntime.AuthenticateWithOptions(ctx, name, config, func(address string) error {
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				if onURL != nil {
					onURL(address)
				}
				return nil
			}, mcpauth.FlowOptions{})
		})
		if err != nil {
			return err
		}
		_, err = pending.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
