package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) initializeNativeCLIMCP() error {
	configs, warnings, err := m.mcpConfigurations.CLI(context.Background(), m.options.Arguments.MCPConfigs, m.options.Arguments.StrictMCPConfig)
	if m.options.Error != nil {
		for _, warning := range warnings {
			if _, writeErr := fmt.Fprintln(m.options.Error, warning); writeErr != nil {
				return writeErr
			}
		}
	}
	if err != nil {
		return err
	}
	m.nativeDynamicMCP = protocol.NewJSONObject()
	sdkConfigs := protocol.NewJSONObject()
	for _, field := range configs.OwnProperties() {
		var config protocol.JSONObject
		if err := config.UnmarshalJSON(field.Value); err != nil {
			return err
		}
		if config.String("type") == "sdk" {
			if err := sdkConfigs.SetPropertyJSON(field.NameJSON, field.Value); err != nil {
				return err
			}
		} else {
			if err := m.nativeDynamicMCP.SetPropertyJSON(field.NameJSON, field.Value); err != nil {
				return err
			}
		}
	}
	if len(sdkConfigs.OwnProperties()) > 0 {
		return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			return m.sdkMCPState.RegisterCLIConfigurations(scope, sdkConfigs)
		})
	}
	return nil
}

func (m *MainPrint) nativeCLIAllMCPConfigurations(ctx context.Context) (mcpconfig.Result, error) {
	// Explicit flags survive bare mode. Strict mode skips every auto-discovery
	// producer, including plugins, user/project config and organization fetches.
	if m.options.Arguments.StrictMCPConfig || pickerTrue(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return mcpconfig.Result{Servers: m.nativeDynamicMCP.Clone(), Errors: []protocol.JSONObject{}}, nil
	}
	var result mcpconfig.Result
	var err error
	if m.claudeAIMCP == nil {
		result, err = m.mcpConfigurations.AllConfigurations(ctx, protocol.NewJSONObject())
	} else {
		result, err = m.claudeAIMCP.All(ctx, m.mcpConfigurations)
	}
	if err == nil {
		result.Servers.Assign(m.nativeDynamicMCP)
	}
	return result, err
}
