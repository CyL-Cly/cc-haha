package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TerminalStatusProperty struct {
	Label string          `json:"label,omitempty"`
	Value json.RawMessage `json:"value"`
}

func statusProperty(label string, value any) TerminalStatusProperty {
	return TerminalStatusProperty{label, rawMain(value)}
}
func terminalPolicySourceLabel(observation NativePolicyObservation) string {
	switch observation.Origin {
	case "remote":
		return "Enterprise managed settings (remote)"
	case "plist":
		return "Enterprise managed settings (plist)"
	case "hklm":
		return "Enterprise managed settings (HKLM)"
	case "hkcu":
		return "Enterprise managed settings (HKCU)"
	case "file":
		if observation.HasBase && observation.HasDropIns {
			return "Enterprise managed settings (file + drop-ins)"
		}
		if observation.HasDropIns {
			return "Enterprise managed settings (drop-ins)"
		}
		return "Enterprise managed settings (file)"
	}
	return ""
}
func terminalStatusMCPProperty(servers []protocol.JSONObject) *TerminalStatusProperty {
	connected, pending, auth, failed := 0, 0, 0, 0
	for _, server := range servers {
		if server.String("name") == "ide" {
			continue
		}
		switch server.String("status") {
		case "connected":
			connected++
		case "pending":
			pending++
		case "needs-auth":
			auth++
		default:
			failed++
		}
	}
	if connected+pending+auth+failed == 0 {
		return nil
	}
	parts := []string{}
	for _, count := range []struct {
		n    int
		text string
	}{{connected, "connected"}, {auth, "need auth"}, {pending, "pending"}, {failed, "failed"}} {
		if count.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count.n, count.text))
		}
	}
	value := statusProperty("MCP servers", strings.Join(parts, ", ")+" · /mcp")
	return &value
}
func (m *MainPrint) TerminalStatus(ctx context.Context) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		properties := []TerminalStatusProperty{statusProperty("Version", m.options.Version)}
		metadata, err := m.Controller.TerminalMetadataOnOwner(scope)
		if err != nil {
			return err
		}
		title := metadata.String("customTitle")
		if title == "" {
			title = "/rename to add a name"
		}
		properties = append(properties, statusProperty("Session name", title), statusProperty("Session ID", m.Controller.SessionID()), statusProperty("cwd", m.workingDirectory()))
		env := m.nativeOpenAIEnvironment()
		facade := &MainCommand{doctorPackages: m.doctorPackages, Owner: m.Owner, Native: m.options.Native, Settings: m.nativeSettings, Startup: m.Startup, Global: m.Global, Secure: m.secure, version: m.options.Version, credentialPlatform: m.options.Platform}
		identity, tokens, keyPresent, enabled, err := m.terminalStatusAuth(ctx, scope)
		if err != nil {
			return err
		}
		if m.openAIAuth != nil {
			openAI, err := m.openAIAuth.GetCached(ctx, scope)
			if err != nil {
				return err
			}
			if openAI != nil {
				identity.OpenAITokens = rawMain(openAI.Object)
			}
		}
		subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
		if err != nil {
			return err
		}
		for _, property := range append(cliauth.AccountProperties(identity, keyPresent, subscriber, env["IS_DEMO"] != ""), authProviderProperties(env, identity.APIProvider)...) {
			properties = append(properties, TerminalStatusProperty{property.Label, property.Value})
		}
		for _, item := range []struct{ key, label string }{{"CLAUDE_CODE_CLIENT_CERT", "mTLS client cert"}, {"CLAUDE_CODE_CLIENT_KEY", "mTLS client key"}} {
			if path := env[item.key]; path != "" {
				absolute := path
				if !filepath.IsAbs(path) {
					absolute = filepath.Join(m.workingDirectory(), path)
				}
				if data, e := m.options.Native.readFile(absolute); e == nil && len(data) > 0 {
					properties = append(properties, statusProperty(item.label, path))
				}
			}
		}
		if err = m.initializeNativeModelCommandState(scope); err != nil {
			return err
		}
		model, err := terminalStatusModelLabel(m.nativeModelSelector, m.modelPicker.rawModel, subscriber, tokens, env)
		if err != nil {
			return err
		}
		properties = append(properties, statusProperty("Model", model))
		servers, err := m.mcpTools.ServerStatuses(ctx, scope)
		if err != nil {
			return err
		}
		for i := range servers {
			if servers[i].String("name") == "ide" {
				config := mainObj(m.nativeDynamicMCP.Get("ide"))
				if config.String("type") == "ws-ide" || config.String("type") == "sse-ide" {
					servers[i].Set("ideName", config.Get("ideName"))
				} else {
					terminal := ideintegration.Terminal(env)
					if ideintegration.SupportedTerminal(terminal, env) {
						servers[i].Set("ideName", quoteMain(ideintegration.IDEName(terminal)))
					}
				}
			}
		}
		var installation *ideintegration.ExtensionStatus
		if m.ideUI != nil {
			installation = m.ideUI.status
		}
		if property := terminalStatusIDEProperty(servers, installation); property != nil {
			properties = append(properties, *property)
		}
		if property := terminalStatusMCPProperty(servers); property != nil {
			properties = append(properties, *property)
		}
		sources := []string{}
		for _, source := range m.Startup.EnabledSettingSources() {
			settings, err := m.Startup.GetSettingsForSource(source)
			if err != nil {
				return err
			}
			if settings == nil || len(settings.OwnProperties()) == 0 {
				continue
			}
			name := map[string]string{"userSettings": "User settings", "projectSettings": "Shared project settings", "localSettings": "Project local settings", "flagSettings": "Command line arguments"}[source]
			if source == "policySettings" {
				name = terminalPolicySourceLabel(m.options.Native.PolicyObservation())
			}
			if name != "" {
				sources = append(sources, name)
			}
		}
		properties = append(properties, statusProperty("Setting sources", sources))
		diagnostic, err := facade.NativeInstallationDiagnostic(ctx, scope)
		m.doctorPackages = facade.doctorPackages
		if err != nil {
			return err
		}
		diagnostics := []string{}
		for _, warning := range diagnostic.Warnings {
			diagnostics = append(diagnostics, warning.Issue)
		}
		if diagnostic.HasUpdatePermissions != nil && !*diagnostic.HasUpdatePermissions {
			diagnostics = append(diagnostics, "No write permissions for auto-updates (requires sudo)")
		}
		if parsed := m.settingState.GetSessionSettingsCache(); parsed != nil && len(parsed.Errors) > 0 {
			files := []string{}
			seen := map[string]bool{}
			for _, raw := range parsed.Errors {
				file := mainObj(raw).String("file")
				if file != "" && !seen[file] {
					files = append(files, file)
					seen[file] = true
				}
			}
			if len(files) > 0 {
				diagnostics = append(diagnostics, "Found invalid settings files: "+strings.Join(files, ", ")+". They will be ignored.")
			}
		}
		memory, err := nativeSkillWork(m, scope, m.userContext.MemoryFilesForInspection)
		if err != nil {
			return err
		}
		files, err := memory.Await(scope)
		if err != nil {
			return err
		}
		for _, file := range files {
			units, err := protocol.DecodeJavaScriptJSONString(file.PromptContent())
			if err != nil {
				return err
			}
			if len(units) > 40000 {
				path := file.Path
				if strings.HasPrefix(path, m.options.Native.Home+string(os.PathSeparator)) {
					path = "~" + strings.TrimPrefix(path, m.options.Native.Home)
				}
				diagnostics = append(diagnostics, fmt.Sprintf("Large %s will impact performance (%s chars > 40,000)", path, statusFormatNumber(len(units))))
			}
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "properties", Value: rawMain(properties)}, protocol.JSONField{Name: "diagnostics", Value: rawMain(diagnostics)})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
func statusFormatNumber(n int) string {
	text := fmt.Sprint(n)
	for at := len(text) - 3; at > 0; at -= 3 {
		text = text[:at] + "," + text[at:]
	}
	return text
}

// Status observes the same cached credential producer used by the live model
// resolver. It never constructs a new auth service or re-reads a descriptor.
func (m *MainPrint) terminalStatusAuth(ctx context.Context, scope *cooperative.Scope) (cliauth.GeneralStatusInput, *openaiauth.Value, bool, bool, error) {
	env := m.nativeOpenAIEnvironment()
	provider := "firstParty"
	for _, pair := range []struct{ key, value string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTruthyNative(env[pair.key]) {
			provider = pair.value
			break
		}
	}
	identity := cliauth.GeneralStatusInput{APIProvider: provider, AuthTokenSource: "none", APIKeySource: "none", UsingThirdParty: provider != "firstParty", HasAPIKeyEnvironment: env["ANTHROPIC_API_KEY"] != ""}
	owned, err := m.openAIAuth.owned(ctx, scope)
	if err != nil {
		return identity, nil, false, false, err
	}
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	settings, err := m.openAIAuth.authDependencies(env, bare)
	if err != nil {
		return identity, nil, false, false, err
	}
	source, err := m.openAIAuth.apiKeySource(owned, env, bare, false)
	if err != nil {
		return identity, nil, false, false, err
	}
	identity.APIKeySource = source
	enabled := openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: bare, Environment: env, APIKeySource: source, APIKeyHelper: settings.Get("apiKeyHelper")})
	tokens, err := m.openAIAuth.claudeTokensFor(owned, env, bare)
	if err != nil {
		return identity, nil, false, false, err
	}
	subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
	if err != nil {
		return identity, nil, false, false, err
	}
	helper := settings.String("apiKeyHelper")
	managed := env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop" || envTruthyNative(env["CLAUDE_CODE_REMOTE"])
	switch {
	case bare:
		if helper != "" {
			identity.AuthTokenSource = "apiKeyHelper"
		}
	case env["ANTHROPIC_AUTH_TOKEN"] != "" && !managed:
		identity.AuthTokenSource = "ANTHROPIC_AUTH_TOKEN"
	case env["CLAUDE_CODE_OAUTH_TOKEN"] != "":
		identity.AuthTokenSource = "CLAUDE_CODE_OAUTH_TOKEN"
	case helper != "" && !managed:
		identity.AuthTokenSource = "apiKeyHelper"
	case subscriber && tokens != nil && openaiauth.AuthTruthy(tokens.Get("accessToken")):
		identity.AuthTokenSource = "claude.ai"
	}
	identity.HasToken = identity.AuthTokenSource != "none"
	global, err := m.Global.Get()
	if err != nil {
		return identity, nil, false, false, err
	}
	if global != nil {
		identity.OAuthAccount = global.Get("oauthAccount")
	}
	if tokens != nil {
		identity.SubscriptionType = tokens.Get("subscriptionType")
	}
	present := source == "ANTHROPIC_API_KEY" && env["ANTHROPIC_API_KEY"] != "" || source == "/login managed key" && m.openAIAuth.legacyAPIKeyPresent
	if source == "apiKeyHelper" && m.apiKeyHelper != nil {
		value := m.apiKeyHelper.Cached()
		present = value != nil && *value != ""
	}
	return identity, tokens, present, enabled, nil
}

func terminalStatusIDEProperty(servers []protocol.JSONObject, installation *ideintegration.ExtensionStatus) *TerminalStatusProperty {
	var server *protocol.JSONObject
	for i := range servers {
		if servers[i].String("name") == "ide" {
			server = &servers[i]
			break
		}
	}
	connected := server != nil && server.String("status") == "connected"
	if installation != nil {
		name := ideintegration.IDEName(installation.IDEType)
		suffix := "extension"
		if ideintegration.JetBrainsIDE(installation.IDEType) {
			suffix = "plugin"
		}
		if installation.Error != nil {
			value := statusProperty("IDE", "× Error installing "+name+" "+suffix+": "+*installation.Error+"\nPlease restart your IDE and try again.")
			return &value
		}
		if installation.Installed {
			label := "Installed " + name + " " + suffix
			if connected {
				version := "undefined"
				if installation.Version != nil {
					version = *installation.Version
				}
				label = "Connected to " + name + " " + suffix + " version " + version
				info := mainObj(server.Get("serverInfo"))
				serverVersion := "undefined"
				if info.Get("version") != nil {
					serverVersion = info.String("version")
				}
				if serverVersion != version {
					label += " (server version: " + serverVersion + ")"
				}
			}
			value := statusProperty("IDE", label)
			return &value
		}
	} else if server != nil {
		name := server.String("ideName")
		if name == "" {
			name = "IDE"
		}
		label := "× Not connected to " + name
		if connected {
			label = "Connected to " + name + " extension"
		}
		value := statusProperty("IDE", label)
		return &value
	}
	return nil
}
func terminalStatusModelLabel(selector *modelselection.Selector, model *string, subscriber bool, tokens *openaiauth.Value, env map[string]string) (string, error) {
	label, err := nativePickerDisplay(selector, model)
	if err != nil || model != nil {
		return label, err
	}
	if env["USER_TYPE"] == "ant" {
		resolved, err := nativePickerResolve(selector, nil)
		if err != nil {
			return "", err
		}
		marketing, err := selector.PublicModelDisplayName(quoteMain(resolved))
		if err != nil {
			return "", err
		}
		if marketing == "" {
			marketing = resolved
		}
		return "Default for Ants (" + marketing + ")", nil
	}
	if subscriber {
		description := "Sonnet 5.5 · Best for everyday tasks"
		if tokens != nil && (tokens.Object.String("subscriptionType") == "max" || tokens.Object.String("subscriptionType") == "team" && tokens.Object.String("rateLimitTier") == "default_claude_max_5x") {
			description = "Opus 5.5"
			thirdParty := false
			for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
				thirdParty = thirdParty || envTruthyNative(env[key])
			}
			if !envTruthyNative(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && !thirdParty {
				description += " with 1M context"
			}
			description += " · Most capable for complex work"
		}
		return "Default " + description, nil
	}
	return label, nil
}
