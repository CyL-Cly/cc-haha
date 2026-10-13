package requestcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const compatibilityVersion = provider.SourceCompatibilityVersion

var modelContextSuffix = regexp.MustCompile(`(?i)\[(1|2)m\]`)
var modelDate = regexp.MustCompile(`-\d{8}$`)
var providerBudgetDigits = regexp.MustCompile(`^\d+$`)
var numericEffort = regexp.MustCompile(`^-?\d+$`)

func canonicalModel(model string) string {
	canonical := modelDate.ReplaceAllString(strings.ToLower(modelContextSuffix.ReplaceAllString(model, "")), "")
	// The pinned Source canonicalizer has no Opus 4.6 branch and resolves it
	// as Opus 4. Preserve the actual 32k limit rather than repairing Source.
	if canonical == "claude-opus-4-6" {
		return "claude-opus-4"
	}
	return canonical
}
func modelLimits(model string) (float64, float64, error) {
	switch canonicalModel(model) {
	case "fixture-model", "deepseek-flash", "claude-sonnet-4", "claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-4-5", "claude-3-7-sonnet":
		return 32000, 64000, nil
	case "claude-sonnet-4-6":
		return 32000, 128000, nil
	case "claude-opus-4", "claude-opus-4-1":
		return 32000, 32000, nil
	case "claude-sonnet-5", "claude-opus-5", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-fable-5-1":
		return 64000, 128000, nil
	case "claude-sonnet-5-5", "claude-opus-5-5":
		return 128000, 128000, nil
	case "claude-3-opus", "claude-3-haiku":
		return 4096, 4096, nil
	case "claude-3-sonnet", "claude-3-5-sonnet", "claude-3-5-haiku":
		return 8192, 8192, nil
	default:
		return 0, 0, unsupported("model catalog or capability override: " + model)
	}
}
func configuredProviderBudget(env map[string]string) *float64 {
	raw := env["CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS"]
	value, _ := strconv.ParseFloat(raw, 64)
	if providerBudgetDigits.MatchString(raw) && safePositive(value) {
		return &value
	}
	return nil
}
func safePositive(value float64) bool {
	return value > 0 && value <= 9007199254740991 && math.Trunc(value) == value
}
func jsNumber(raw string) float64 {
	raw = trim(raw)
	if raw == "" {
		return 0
	}
	if len(raw) > 2 && raw[0] == '0' {
		base := 0
		switch raw[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			value, err := strconv.ParseUint(raw[2:], base, 64)
			if err == nil {
				return float64(value)
			}
			return math.NaN()
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil && !math.IsInf(value, 0) {
		return math.NaN()
	}
	return value
}
func jsParseInt(raw string) float64 {
	raw = trim(raw)
	length := 0
	if len(raw) > 0 && (raw[0] == '-' || raw[0] == '+') {
		length++
	}
	first := length
	for length < len(raw) && raw[length] >= '0' && raw[length] <= '9' {
		length++
	}
	if first == length {
		return math.NaN()
	}
	value, _ := strconv.ParseFloat(raw[:length], 64)
	return value
}
func maxTokens(input Context, fallback, upper float64) float64 {
	if override := input.Options.MaxOutputTokensOverride; override != nil && *override != 0 && !math.IsNaN(*override) {
		return *override
	}
	global := input.Host.Env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"]
	if !safePositive(jsNumber(global)) {
		if provider := configuredProviderBudget(input.Host.Env); provider != nil {
			return *provider
		}
	}
	parsed := jsParseInt(global)
	if math.IsNaN(parsed) || parsed <= 0 {
		return fallback
	}
	return math.Min(parsed, upper)
}
func cacheControl(host Host, querySource, scope string) json.RawMessage {
	value := object(field("type", quoted("ephemeral")))
	if host.TTLEligible {
		for _, pattern := range host.TTLAllowlist {
			if pattern == querySource || strings.HasSuffix(pattern, "*") && strings.HasPrefix(querySource, strings.TrimSuffix(pattern, "*")) {
				value.Set("ttl", quoted("1h"))
				break
			}
		}
	}
	if scope == "global" {
		value.Set("scope", quoted("global"))
	}
	return rawObject(value)
}
func fingerprint(history []protocol.JSONObject) string {
	var text json.RawMessage = quoted("")
	for _, envelope := range history {
		if envelope.String("type") != "user" {
			continue
		}
		value := content(envelope)
		if len(value) > 0 && value[0] == '"' {
			text = value
		} else {
			for _, block := range blocks(value) {
				if block.String("type") == "text" {
					text = block.Get("text")
					break
				}
			}
		}
		break
	}
	units, _ := protocol.DecodeJavaScriptJSONString(text)
	chars := []uint16{}
	for _, index := range []int{4, 7, 20} {
		if index < len(units) {
			chars = append(chars, units[index])
		} else {
			chars = append(chars, '0')
		}
	}
	// crypto.update encodes the selected JS string as UTF-8, replacing any
	// unpaired surrogate only here; original request text remains lossless.
	selected := stringValue(protocol.QuoteJavaScriptUTF16(chars))
	hash := sha256.Sum256([]byte("59cf53e54c78" + selected + compatibilityVersion))
	return hex.EncodeToString(hash[:])[:3]
}

const cliPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
const sdkPrefix = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
const sdkPresetPrefix = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."

func buildSystem(input Context, history []protocol.JSONObject, caching bool) (json.RawMessage, error) {
	prompts := []json.RawMessage{}
	if !envFalse(input.Host.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"]) {
		workload := ""
		if input.Host.Workload != "" {
			workload = " cc_workload=" + input.Host.Workload + ";"
		}
		prompts = append(prompts, quoted("x-anthropic-billing-header: cc_version="+compatibilityVersion+"."+fingerprint(history)+"; cc_entrypoint=cli; cch=00000;"+workload))
	}
	prefix := cliPrefix
	if input.Options.IsNonInteractiveSession {
		prefix = sdkPrefix
		if input.Options.HasAppendSystemPrompt {
			prefix = sdkPresetPrefix
		}
	}
	prompts = append(prompts, quoted(prefix))
	prompts = append(prompts, input.SystemPrompt...)
	var attribution, systemPrefix json.RawMessage
	rest := []json.RawMessage{}
	for _, prompt := range prompts {
		if _, err := protocol.DecodeJavaScriptJSONString(prompt); err != nil {
			return nil, unsupported("system prompt must be a JavaScript string")
		}
		if !truthy(prompt) {
			continue
		}
		text := stringValue(prompt)
		if strings.HasPrefix(text, "x-anthropic-billing-header") {
			attribution = prompt
		} else if text == cliPrefix || text == sdkPrefix || text == sdkPresetPrefix {
			systemPrefix = prompt
		} else {
			rest = append(rest, prompt)
		}
	}
	result := []json.RawMessage{}
	add := func(text json.RawMessage, cache bool) {
		if !truthy(text) {
			return
		}
		block := object(field("type", quoted("text")), field("text", text))
		if cache && caching {
			block.Set("cache_control", cacheControl(input.Host, input.Options.QuerySource, "org"))
		}
		result = append(result, rawObject(block))
	}
	add(attribution, false)
	add(systemPrefix, true)
	joined, err := protocol.JoinJavaScriptValuesJSON(rest, "\n\n")
	if err != nil {
		return nil, err
	}
	add(joined, true)
	return rawArray(result), nil
}

func objectEnvironment(raw string) protocol.JSONObject {
	parsed, err := parseObject(json.RawMessage(raw))
	if err != nil || !parsed.IsObject() {
		return object()
	}
	return parsed
}
func metadata(input Context) (json.RawMessage, error) {
	if raw := input.Host.Env["CLAUDE_CODE_EXTRA_METADATA"]; raw != "" && !json.Valid([]byte(raw)) {
		return nil, unsupported("malformed extra metadata Source error")
	}
	extra := objectEnvironment(input.Host.Env["CLAUDE_CODE_EXTRA_METADATA"])
	extra.Set("device_id", quoted(input.Host.DeviceID))
	account := ""
	if input.Host.OAuthAccountUUID != nil {
		account = *input.Host.OAuthAccountUUID
	} else {
		source := ""
		if input.Host.Env["ANTHROPIC_API_KEY"] != "" {
			source = "ANTHROPIC_API_KEY"
		}
		if openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: envTrue(input.Host.Env["CLAUDE_CODE_SIMPLE"]), Environment: input.Host.Env, APIKeySource: source}) {
			account = trim(input.Host.Env["CLAUDE_CODE_ACCOUNT_UUID"])
		}
	}
	extra.Set("account_uuid", quoted(account))
	extra.Set("session_id", quoted(input.Host.SessionID))
	serialized, err := stringify(rawObject(extra))
	if err != nil {
		return nil, err
	}
	return rawObject(object(field("user_id", quoted(string(serialized))))), nil
}
func resolveThinking(input Context, max, upper float64) (json.RawMessage, bool) {
	model := canonicalModel(input.Options.Model)
	required := model == "claude-sonnet-5-5" || model == "claude-opus-5-5" || model == "claude-fable-5-1"
	canThink := required || strings.Contains(model, "sonnet-4") || strings.Contains(model, "opus-4")
	enabled := required || input.Thinking.Type != "disabled" && !envTrue(input.Host.Env["CLAUDE_CODE_DISABLE_THINKING"])
	hasThinking := (required || max > 1024) && enabled
	if hasThinking && canThink {
		if required {
			result := object(field("type", quoted("adaptive")))
			if model == "claude-fable-5-1" {
				result.Set("block_binding", json.RawMessage(`{"prefix_mismatch_behavior":"drop_block"}`))
			}
			return rawObject(result), true
		}
		budget := upper - 1
		if input.Thinking.Type == "enabled" && input.Thinking.BudgetTokens != nil {
			budget = *input.Thinking.BudgetTokens
		}
		return rawObject(object(field("budget_tokens", rawNumber(math.Min(max-1, budget))), field("type", quoted("enabled")))), true
	}
	if !hasThinking && (canThink || envTrue(input.Host.Env["CC_HAHA_SEND_DISABLED_THINKING"])) {
		return json.RawMessage(`{"type":"disabled"}`), false
	}
	return nil, hasThinking
}

func appliedEffort(input Context) (string, bool) {
	requested := input.Options.EffortValue
	if !input.Options.EffortValueOverridesEnv || requested == nil {
		raw := input.Host.Env["CLAUDE_CODE_EFFORT_LEVEL"]
		if strings.EqualFold(raw, "unset") || strings.EqualFold(raw, "auto") {
			return "high", false
		}
		normalized := strings.ToLower(trim(raw))
		switch normalized {
		case "low", "medium", "high", "xhigh", "max":
			requested = quoted(normalized)
		default:
			if numericEffort.MatchString(normalized) {
				value := jsNumber(normalized)
				if math.Trunc(value) == value && !math.IsInf(value, 0) {
					return "", true
				}
			}
		}
	}
	if len(requested) > 0 && requested[0] != '"' {
		return "", true
	}
	effort := stringValue(requested)
	if effort == "" {
		effort = "high"
		model := canonicalModel(input.Options.Model)
		if model == "claude-sonnet-5-5" || model == "claude-opus-5-5" {
			effort = "medium"
		}
	}
	if effort == "xhigh" || effort == "max" {
		effort = "high"
	}
	return effort, false
}
