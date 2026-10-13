package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/installationdiagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

func (m *MainPrint) NativeDoctorEnabled() bool {
	return m != nil && m.options.Native != nil && !envTruthyNative(m.options.Native.Environment["DISABLE_DOCTOR_COMMAND"])
}
func (m *MainPrint) NativeDoctorCommand(ctx context.Context) (string, error) {
	if !m.NativeDoctorEnabled() {
		return "", mainCap("disabled native /doctor command")
	}
	var text string
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.NativeDoctorEnabled() {
			return mainCap("disabled native /doctor command")
		}
		if m.closed || !m.initialized || m.Host == nil {
			return cooperative.ErrClosed
		}
		facade := &MainCommand{doctorPackages: m.doctorPackages, Owner: m.Owner, Native: m.options.Native, Settings: m.nativeSettings, Startup: m.Startup, Global: m.Global, Secure: m.secure, version: m.options.Version, credentialPlatform: m.options.Platform}
		base, e := facade.nativeDoctorDiagnosticText(ctx, scope)
		m.doctorPackages = facade.doctorPackages
		if e != nil {
			return e
		}
		out := strings.Builder{}
		out.WriteString(base)
		tls, e := tlsconfig.NewState(m.options.Native.readFile)
		if e != nil {
			return e
		}
		client, e := webfetch.NewHTTPClient(m.options.Native.Environment, tls)
		if e != nil {
			return e
		}
		repo := cliinstallation.RepositoryClient{HTTP: client, APIBase: m.options.Native.Environment["CC_HAHA_CLI_RELEASE_API"]}
		channelWork, e := nativeSkillWork(m, scope, func() (nativeDoctorChannels, error) { return probeNativeDoctorChannels(ctx, repo), nil })
		if e != nil {
			return e
		}
		channels, e := channelWork.Await(scope)
		if e != nil {
			return e
		}
		out.WriteString(nativeDoctorChannelsText(channels))
		for _, warning := range installationdiagnostics.EnvironmentWarnings(m.options.Native.Environment) {
			fmt.Fprintf(&out, "Environment variable limits\n%s\n", warning.Issue)
		}
		if parsed := m.settingState.GetSessionSettingsCache(); parsed != nil {
			for _, issue := range parsed.Errors {
				fmt.Fprintf(&out, "Settings validation: %s\n", string(issue))
			}
		}
		in := installationdiagnostics.ContextInput{}
		in.Permission, e = m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		settings, e := m.Startup.GetInitialSettings()
		if e != nil {
			return e
		}
		sandbox := protocol.NewJSONObject()
		if settings != nil {
			_ = sandbox.UnmarshalJSON(settings.Get("sandbox"))
		}
		in.SandboxAutoAllow = m.sandbox != nil && m.sandbox.manager != nil && string(sandbox.Get("autoAllowBashIfSandboxed")) != "false"
		memory, e := nativeSkillWork(m, scope, m.userContext.MemoryFilesForInspection)
		if e != nil {
			return e
		}
		in.Memory, e = memory.Await(scope)
		if e != nil {
			return e
		}
		if m.agents != nil {
			agents, e := m.agents.List(scope)
			if e != nil {
				return e
			}
			in.Agents = agents.Active
			out.WriteString("\nAgents\n")
			for _, agent := range agents.Active {
				fmt.Fprintf(&out, "%s (%s)\n", agent.Name, agent.Source)
			}
			for _, failed := range agents.FailedFiles {
				fmt.Fprintf(&out, "Warning: %s: %s\n", failed.Path, failed.Error)
			}
		}
		actual, e := m.Host.SnapshotContextInspection(scope)
		if e != nil {
			return e
		}
		for i, definition := range actual.Definitions {
			if definition.IsMCP {
				schema := actual.Schemas[i]
				in.MCP = append(in.MCP, installationdiagnostics.MCPTool{Name: definition.Name, Description: schema.String("description"), Schema: schema, AlwaysLoad: definition.AlwaysLoad})
			}
		}
		if len(in.MCP) > 0 {
			counter, e := m.nativeTokenCounter(ctx, scope, actual.MainModel)
			if e != nil {
				return e
			}
			in.Count = func(schemas []protocol.JSONObject) (*float64, error) { return counter(nil, schemas) }
			// Doctor passes AppState.mcp.tools, not the complete ASK registry,
			// to isToolSearchEnabled. That array has no built-in ToolSearch.
			in.Deferred = false
		}
		warnings := installationdiagnostics.Context(in)
		out.WriteString("\nContext diagnostics\n")
		for _, warning := range []*installationdiagnostics.ContextWarning{warnings.Memory, warnings.Agents, warnings.MCP, warnings.Rules} {
			if warning == nil {
				continue
			}
			fmt.Fprintf(&out, "Warning: %s\n", warning.Message)
			for _, detail := range warning.Details {
				fmt.Fprintf(&out, "  %s\n", detail)
			}
		}
		if m.sandbox != nil {
			errors := append([]string{}, m.sandbox.dependencies...)
			warnings := []string{}
			if m.sandbox.platform == "linux" || m.sandbox.platform == "wsl" {
				warnings = sandboxruntime.CheckLinuxDependencies(m.sandbox.linux.Status).Warnings
			}
			out.WriteString(nativeDoctorSandboxText(m.sandbox.supported, string(sandbox.Get("enabled")) == "true", errors, warnings))
		}
		mcpWork, e := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
			issues := []protocol.JSONObject{}
			for _, source := range []string{"user", "project", "local"} {
				result, e := m.mcpConfigurations.Scope(ctx, source)
				if e != nil {
					return nil, e
				}
				issues = append(issues, result.Errors...)
			}
			return issues, nil
		})
		if e != nil {
			return e
		}
		mcpIssues, e := mcpWork.Await(scope)
		if e != nil {
			return e
		}
		if len(mcpIssues) > 0 {
			out.WriteString("\nMCP Configuration Issues\n")
			for _, issue := range mcpIssues {
				fmt.Fprintf(&out, "  %s: %s\n", issue.String("path"), issue.String("message"))
			}
		}

		plugins, e := m.nativeSettings.LoadPlugins(ctx, "", false)
		if e != nil {
			return e
		}
		out.WriteString("\nPlugins\n")
		for _, plugin := range plugins.Enabled {
			fmt.Fprintf(&out, "%s (%s)\n", plugin.Name, plugin.Source)
		}
		for _, failure := range plugins.Errors {
			fmt.Fprintf(&out, "Warning: %s: %s\n", failure.String("source"), pluginsapi.ErrorMessage(failure))
		}
		installer, e := cliinstallation.New(cliinstallation.Options{Home: m.options.Native.Home, Environment: m.options.Native.Environment, Platform: m.options.Platform})
		if e != nil {
			return e
		}
		encoded, e := scope.AwaitBytes(cooperative.TranscriptOpenStat, func(worker context.Context) ([]byte, error) { return json.Marshal(installer.DiagnosticLocks(worker)) })
		if e != nil {
			return e
		}
		var locks cliinstallation.LockDiagnostic
		if e = json.Unmarshal(encoded, &locks); e != nil {
			return e
		}
		if locks.Enabled {
			out.WriteString("\nVersion Locks\n")
			if locks.Cleaned > 0 {
				fmt.Fprintf(&out, "Cleaned %d stale lock(s)\n", locks.Cleaned)
			}
			if len(locks.Locks) == 0 {
				out.WriteString("No active version locks\n")
			}
			for _, lock := range locks.Locks {
				fmt.Fprintf(&out, "%s: PID %d, running %t, %s\n", lock.Version, lock.PID, lock.IsProcessRunning, lock.Executable)
			}
		}
		text = out.String()
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return text, e
}
