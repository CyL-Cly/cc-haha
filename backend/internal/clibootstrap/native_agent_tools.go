package clibootstrap

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// CustomAgentTools mirrors the server's getAllBaseTools/isEnabled projection.
// It declares configurable Source names; executable catalogs still require
// registered handlers and retain their complete-catalog admission boundary.
func (p *NativeProcessSettings) CustomAgentTools(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, mainCap("closed server Agent tool producer")
	}
	settings, err := p.initialSettings()
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, field := range p.environment.Snapshot().OwnProperties() {
		var name, value string
		if json.Unmarshal(field.NameJSON, &name) == nil && json.Unmarshal(field.Value, &value) == nil {
			env[name] = value
		}
	}
	var model string
	if resolver := p.agentModelResolver; resolver != nil {
		model, err = resolver(ctx, settings.Clone(), env)
	} else {
		model, _, err = resolveNativeModel(settings, env, "")
	}
	if err != nil {
		return nil, err
	}
	if windowshell.IsWindows(p.platform) {
		if p.shells == nil {
			p.shells = windowshell.NewNative(p.platform, p.n.CWD, env)
		}
		view := p.shells.Warm(ctx)
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		return nativeCustomAgentToolNames(p.platform, env, settings, model, view.Availability)
	}
	return nativeCustomAgentToolNames(p.platform, env, settings, model)
}

func nativeCustomAgentToolNames(platform string, env map[string]string, settings *protocol.JSONObject, model string, shells ...windowshell.Availability) ([]string, error) {
	availability := windowshell.Availability{Bash: true}
	if windowshell.IsWindows(platform) {
		if len(shells) != 1 {
			return nil, mainCap("actual Agent available-tools Windows shell discovery")
		}
		availability = shells[0]
	} else if platform != "darwin" && platform != "linux" {
		return nil, mainCap("Agent available-tools platform " + platform)
	}
	names := []string{}
	if availability.Bash {
		names = append(names, "Bash")
	}
	embedded := nativeEnvTruthy(env["EMBEDDED_SEARCH_TOOLS"])
	switch env["CLAUDE_CODE_ENTRYPOINT"] {
	case "sdk-ts", "sdk-py", "sdk-cli", "local-agent":
		embedded = false
	}
	if !embedded {
		names = append(names, "Glob", "Grep")
	}
	names = append(names, "Read", "Edit", "Write", "NotebookEdit", "WebFetch")
	// The local API server is a noninteractive Source process; an explicit
	// ENABLE_TASKS opt-in suppresses TodoWrite even there.
	if !nativeEnvTruthy(env["CLAUDE_CODE_ENABLE_TASKS"]) {
		names = append(names, "TodoWrite")
	}
	if nativeAgentWebSearchEnabled(settings, model) {
		names = append(names, "WebSearch")
	}
	names = append(names, "Skill", "EnterWorktree", "ExitWorktree")
	if nativeAgentToolSearchEnabled(env) {
		names = append(names, "ToolSearch")
	}
	if availability.PowerShell {
		names = append(names, "PowerShell")
	}
	definition := agentcatalog.Definition{Fields: protocol.NewJSONObject()}
	resolved := agentcatalog.ResolveTools(definition, names, true, false).ResolvedTools
	sort.Strings(resolved) // These Source names contain only ASCII; localeCompare agrees.
	return resolved, nil
}

var nativeClaudeModel = regexp.MustCompile(`(?i)(^|[/:._-])claude([/:._-]|$)`)

func nativeAgentWebSearchEnabled(settings *protocol.JSONObject, model string) bool {
	web := protocol.NewJSONObject()
	if settings != nil {
		_ = web.UnmarshalJSON(settings.Get("webSearch"))
	}
	mode := web.String("mode")
	tavily := nativeAgentJSTrim(web.String("tavilyApiKey")) != ""
	brave := nativeAgentJSTrim(web.String("braveApiKey")) != ""
	claude := nativeClaudeModel.MatchString(model)
	switch mode {
	case "disabled":
		return false
	case "tavily":
		return tavily
	case "brave":
		return brave
	case "anthropic":
		return claude
	default:
		return claude || tavily || brave
	}
}

func nativeAgentToolSearchEnabled(env map[string]string) bool {
	if nativeEnvTruthy(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]) {
		return false
	}
	value := env["ENABLE_TOOL_SEARCH"]
	if nativeEnvFalsy(value) {
		return false
	}
	if strings.HasPrefix(value, "auto:") {
		suffix := nativeAgentJSTrim(strings.TrimPrefix(value, "auto:"))
		sign, n := "", 0
		if strings.HasPrefix(suffix, "+") || strings.HasPrefix(suffix, "-") {
			sign, suffix = suffix[:1], suffix[1:]
		}
		for n < len(suffix) && suffix[n] >= '0' && suffix[n] <= '9' {
			n++
		}
		if n > 0 {
			number, _ := strconv.ParseFloat(sign+suffix[:n], 64)
			if number >= 100 {
				return false
			}
		}
	}
	if value != "" {
		return true
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if nativeEnvTruthy(env[name]) {
			return true
		}
	}
	base := env["ANTHROPIC_BASE_URL"]
	if base == "" {
		return true
	}
	parsed, err := weburl.Parse(base)
	if err != nil {
		return false
	}
	return parsed.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com"
}

func nativeEnvTruthy(value string) bool {
	switch strings.ToLower(nativeAgentJSTrim(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func nativeEnvFalsy(value string) bool {
	switch strings.ToLower(nativeAgentJSTrim(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func nativeAgentJSTrim(value string) string {
	return strings.Trim(value, " \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}
