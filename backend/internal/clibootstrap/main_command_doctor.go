package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/doctorapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// Doctor runs the actual catalog and MCP health producers without an ASK,
// conversation or model request. Probe processes belong to this command and
// are closed before the command returns, including failed startup probes.
func (m *MainCommand) Doctor(ctx context.Context, scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return "", cooperative.ErrRestricted
	}
	text, err := m.nativeDoctorDiagnosticText(ctx, scope)
	if err != nil {
		return "", err
	}
	reportService, err := doctorapi.New(doctorapi.Options{HomeDir: m.Native.Home, ConfigDir: m.Native.Environment["CLAUDE_CONFIG_DIR"], ProjectRoot: m.Native.CWD, UsesConfigDirOverride: m.Native.Environment["CLAUDE_CONFIG_DIR"] != ""})
	if err != nil {
		return "", err
	}
	payload, err := scope.AwaitBytes(cooperative.TranscriptOpenStat, func(worker context.Context) ([]byte, error) {
		report, err := reportService.GetReport(worker)
		if err != nil {
			return nil, err
		}
		return json.Marshal(report)
	})
	if err != nil {
		return "", err
	}
	var report doctorapi.Report
	if err = json.Unmarshal(payload, &report); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(text)
	channels, err := m.nativeDoctorChannelsText(scope)
	if err != nil {
		return "", err
	}
	out.WriteString(channels)
	out.WriteString("\nConfiguration\n")
	for _, item := range report.Items {
		if item.Exists {
			fmt.Fprintf(&out, "%s: %s\n", item.Label, item.Status)
		}
	}
	catalog, err := m.Settings.AgentCatalog(m.Owner, m.Native.CWD, false)
	if err != nil {
		return "", err
	}
	agents, err := catalog.List(scope)
	if err != nil {
		return "", err
	}
	out.WriteString("\nAgents\n")
	for _, definition := range agents.Active {
		fmt.Fprintf(&out, "%s (%s)\n", definition.Name, definition.Source)
	}
	for _, failure := range agents.FailedFiles {
		fmt.Fprintf(&out, "Warning: %s: %s\n", failure.Path, failure.Error)
	}
	claudeAI, err := m.NewCLIClaudeAIMCP(ctx, nil)
	if err != nil {
		return "", err
	}
	options, err := m.Settings.PluginOptions(m.Secure)
	if err != nil {
		return "", err
	}
	plugins, _, err := m.Settings.PluginMCP(options, m.Secure)
	if err != nil {
		return "", err
	}
	// Doctor's original interactive MCP manager skips the trust dialog and
	// discovers configured project stdio peers for health checks.
	configurations, err := m.Settings.MCPConfigurations(plugins, false)
	if err != nil {
		return "", err
	}
	settings := func(worker context.Context) (protocol.JSONObject, error) {
		if err := worker.Err(); err != nil {
			return protocol.JSONObject{}, err
		}
		value, err := m.Settings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}
	platform := m.credentialPlatform
	if platform == "windows" {
		platform = "win32"
	}
	auth, err := mcpauth.New(mcpauth.Options{Secure: m.Secure, Home: m.Native.Home, ConfigDir: m.Native.Environment["CLAUDE_CONFIG_DIR"], CWD: m.Native.CWD, Platform: platform, Environment: m.Native.Environment, Settings: settings})
	if err != nil {
		return "", err
	}
	peers, err := mcpruntime.New(mcpruntime.Options{PrepareHTTP: claudeAI.PrepareHTTP, CWD: m.Native.CWD, Version: m.version, Environment: m.Native.Environment, Enabled: m.Settings.MCPExecutionEnabled, Authenticate: auth.Authenticate, ClearOAuth: auth.Clear, Auth: func(worker context.Context, name string, config protocol.JSONObject) (mcpclient.AuthProvider, error) {
		if config.String("type") == "http" || config.String("type") == "sse" {
			return auth.Provider(worker, name, config)
		}
		return nil, nil
	}})
	if err != nil {
		return "", err
	}
	defer peers.Close(context.Background())
	type healthReport struct {
		Text  string
		Tools []installationdiagnostics.MCPTool
	}
	data, err := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) ([]byte, error) {
		all, err := claudeAI.All(worker, configurations)
		if err != nil {
			return nil, err
		}
		var result strings.Builder
		tools := []installationdiagnostics.MCPTool{}
		seen := map[string]bool{}
		result.WriteString("\nMCP health\n")
		for _, failure := range all.Errors {
			fmt.Fprintf(&result, "Warning: %s\n", string(rawMain(failure)))
		}
		for _, property := range all.Servers.OwnProperties() {
			var config protocol.JSONObject
			if err := config.UnmarshalJSON(property.Value); err != nil {
				return nil, err
			}
			var name string
			if err := json.Unmarshal(property.NameJSON, &name); err != nil {
				return nil, err
			}
			connection, err := peers.Connect(worker, name, config)
			status, count := connection.Status, len(connection.Tools)
			if err != nil {
				fmt.Fprintf(&result, "%s: failed (%s)\n", name, err)
			} else {
				fmt.Fprintf(&result, "%s: %s (%d tools)\n", name, status, count)
				for _, metadata := range connection.Tools {
					toolName := mcpclient.ToolName(name, metadata.String("name"))
					if seen[toolName] {
						continue
					}
					seen[toolName] = true
					prompt := metadata.Get("description")
					if prompt == nil {
						prompt = quoteMain("")
					}
					prompt = mcpclient.TruncateDescription(prompt, mainDoctorDescriptionLimit(m.Native.Environment))
					definition := toolschema.Definition{Kind: "registered", Name: toolName, IsMCP: true, Prompt: prompt, Description: metadata.Get("description"), InputJSONSchema: metadata.Get("inputSchema"), InputSchema: &toolschema.Schema{Kind: "object", UnknownKeys: "passthrough"}}
					schemas, err := toolschema.NewSession().RenderBatch([]toolschema.Definition{definition}, toolschema.Options{Host: toolschema.Host{BaseURL: m.Native.Environment["ANTHROPIC_BASE_URL"], DisableExperimentalBetas: envTruthyNative(m.Native.Environment["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"])}})
					if err != nil {
						return nil, err
					}
					meta := protocol.NewJSONObject()
					_ = meta.UnmarshalJSON(metadata.Get("_meta"))
					tools = append(tools, installationdiagnostics.MCPTool{Name: toolName, Description: schemas[0].String("description"), Schema: schemas[0], AlwaysLoad: string(meta.Get("anthropic/alwaysLoad")) == "true"})
				}
			}
		}
		return json.Marshal(healthReport{Text: result.String(), Tools: tools})
	})
	if err != nil {
		return "", err
	}
	var health healthReport
	if err := json.Unmarshal(data, &health); err != nil {
		return "", err
	}
	out.WriteString(health.Text)
	screen, err := m.nativeDoctorScreen(ctx, scope, installationdiagnostics.ContextInput{Agents: agents.Active, MCP: health.Tools})
	if err != nil {
		return "", err
	}
	out.WriteString(screen)
	return out.String(), nil
}
