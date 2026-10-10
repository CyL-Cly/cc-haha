package teamspawn

import (
	"strings"
	"unicode"
)

type PaneIdentity struct {
	AgentID, AgentName, TeamName, Color, ParentSessionID, AgentType string
	PlanModeRequired                                                bool
}
type PaneCommandOptions struct {
	Identity                                                                                          PaneIdentity
	CWD, Binary, PermissionMode, Effort, Thinking, ThinkingBudget, ModelOverride, Model, SettingsPath string
	SessionBypass                                                                                     bool
	InlinePlugins                                                                                     []string
	Chrome                                                                                            *bool
	Environment                                                                                       map[string]string
}

// ShellQuote copies the installed shell-quote producer, including its drive
// prefix handling. This command string is sent as one backend argument.
func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	whitespace := false
	for _, r := range value {
		if unicode.IsSpace(r) || r == '\ufeff' {
			whitespace = true
		}
	}
	if (strings.ContainsAny(value, "\"\\") || whitespace) && !strings.Contains(value, "'") {
		return "'" + value + "'"
	}
	if strings.ContainsAny(value, "\"'") || whitespace {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range value {
			if strings.ContainsRune("\"\\$`!", r) {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		b.WriteByte('"')
		return b.String()
	}
	var b strings.Builder
	for i, r := range value {
		if strings.ContainsRune("#!\"$&'()*,:;<=>?@[\\]^`{|}", r) && !(r == ':' && i == 1 && len(value) > 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

var teammateEnvironmentKeys = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_EFFORT_LEVEL", "CC_HAHA_OPENAI_REASONING_EFFORT", "CLAUDE_CODE_ALWAYS_ENABLE_EFFORT", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "CLAUDE_CODE_DISABLE_THINKING", "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING", "CC_HAHA_SEND_DISABLED_THINKING", "DISABLE_INTERLEAVED_THINKING", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_BASE_URL", "AZURE_OPENAI_BASE_URL", "AZURE_OPENAI_ENDPOINT", "AZURE_OPENAI_API_VERSION", "AZURE_OPENAI_API_KEY", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_REMOTE", "CLAUDE_CODE_REMOTE_MEMORY_DIR", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"}

func InheritedPaneEnvironment(environment map[string]string) string {
	rows := []string{"CLAUDECODE=1", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1"}
	for _, key := range teammateEnvironmentKeys {
		if value := environment[key]; value != "" {
			rows = append(rows, key+"="+ShellQuote(value))
		}
	}
	return strings.Join(rows, " ")
}
func PaneSpawnCommand(o PaneCommandOptions) string {
	id := o.Identity
	args := []string{"--agent-id " + ShellQuote(id.AgentID), "--agent-name " + ShellQuote(id.AgentName), "--team-name " + ShellQuote(id.TeamName), "--agent-color " + ShellQuote(id.Color), "--parent-session-id " + ShellQuote(id.ParentSessionID)}
	if id.PlanModeRequired {
		args = append(args, "--plan-mode-required")
	}
	if id.AgentType != "" {
		args = append(args, "--agent-type "+ShellQuote(id.AgentType))
	}
	flags := []string{}
	if !id.PlanModeRequired {
		if o.PermissionMode == "bypassPermissions" || o.SessionBypass {
			flags = append(flags, "--dangerously-skip-permissions")
		} else if o.PermissionMode == "acceptEdits" || o.PermissionMode == "auto" {
			flags = append(flags, "--permission-mode "+o.PermissionMode)
		}
	}
	if o.Effort != "" {
		flags = append(flags, "--effort "+ShellQuote(o.Effort))
	}
	switch o.Thinking {
	case "disabled", "adaptive":
		flags = append(flags, "--thinking "+o.Thinking)
	case "enabled":
		flags = append(flags, "--max-thinking-tokens "+o.ThinkingBudget)
	}
	if o.ModelOverride != "" {
		flags = append(flags, "--model "+ShellQuote(o.ModelOverride))
	}
	if o.SettingsPath != "" {
		flags = append(flags, "--settings "+ShellQuote(o.SettingsPath))
	}
	for _, path := range o.InlinePlugins {
		flags = append(flags, "--plugin-dir "+ShellQuote(path))
	}
	if o.Chrome != nil {
		if *o.Chrome {
			flags = append(flags, "--chrome")
		} else {
			flags = append(flags, "--no-chrome")
		}
	}
	inherited := strings.Join(flags, " ")
	if o.Model != "" {
		tokens := strings.Split(inherited, " ")
		filtered := []string{}
		for i, token := range tokens {
			if token != "--model" && (i == 0 || tokens[i-1] != "--model") {
				filtered = append(filtered, token)
			}
		}
		inherited = strings.Join(filtered, " ")
		if inherited != "" {
			inherited += " "
		}
		inherited += "--model " + ShellQuote(o.Model)
	}
	if inherited != "" {
		inherited = " " + inherited
	}
	return "cd " + ShellQuote(o.CWD) + " && env " + InheritedPaneEnvironment(o.Environment) + " " + ShellQuote(o.Binary) + " " + strings.Join(args, " ") + inherited
}
