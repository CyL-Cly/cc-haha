package requestcontext

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// NativeModelValidationRequest retains validateModel's fixed sideQuery call.
// It receives original model/environment/settings, never a prepared request.
func NativeModelValidationRequest(input Context, settings protocol.JSONObject) (protocol.JSONObject, error) {
	model, env := input.Options.Model, input.Host.Env
	selector, err := modelselection.NewParsing(modelselection.Options{Environment: env, Settings: settings})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	canonicalRaw, err := selector.Canonical(quoted(model))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	canonical := stringValue(canonicalRaw)
	provider := "firstParty"
	for _, row := range []struct{ key, name string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTrue(env[row.key]) {
			provider = row.name
			break
		}
	}
	trusted := env["ANTHROPIC_BASE_URL"] == "" || input.Host.NativeClaudeSubscriber
	if u, e := weburl.Parse(env["ANTHROPIC_BASE_URL"]); e == nil && u.Host() == "api.anthropic.com" {
		trusted = true
	}
	first := (provider == "firstParty" && trusted || provider == "foundry") && !envTrue(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"])
	betas := []string{}
	if !strings.Contains(canonical, "haiku") {
		betas = append(betas, "claude-code-20250219")
	}
	if input.Host.NativeClaudeSubscriber {
		betas = append(betas, "oauth-2025-04-20")
	}
	if !envTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (strings.Contains(strings.ToLower(model), "[1m]") || strings.HasSuffix(strings.ToLower(model), ":1m")) {
		betas = append(betas, "context-1m-2025-08-07")
	}
	isp, present := nativeCapability(input, "interleaved_thinking")
	if !present {
		isp = provider == "foundry" || provider == "firstParty" && trusted && !strings.Contains(canonical, "claude-3-") || provider != "firstParty" && (strings.Contains(canonical, "claude-opus-4") || strings.Contains(canonical, "claude-sonnet-4"))
	}
	if isp && !envTrue(env["DISABLE_INTERLEAVED_THINKING"]) {
		betas = append(betas, "interleaved-thinking-2025-05-14")
	}
	if input.Host.NativeThinkingSummaryBeta != nil {
		betas = insertThinkingSummaryBeta(betas, input.Host.NativeThinkingSummaryBeta)
	} else if first && isp && !truthy(settings.Get("showThinkingSummaries")) {
		betas = append(betas, "redact-thinking-2026-02-12")
	}
	management := provider == "foundry" || provider == "firstParty" && trusted && !strings.Contains(canonical, "claude-3-") || provider != "firstParty" && containsAny(canonical, "claude-fable-5", "claude-sonnet-5", "claude-opus-4", "claude-sonnet-4", "claude-haiku-4")
	if first && management {
		betas = append(betas, "context-management-2025-06-27")
	}
	if provider == "vertex" && containsAny(canonical, "claude-opus-4", "claude-sonnet-4", "claude-haiku-4") || provider == "foundry" {
		betas = append(betas, "web-search-2025-03-05")
	}
	if first {
		betas = append(betas, "prompt-caching-scope-2026-01-05")
	}
	for _, beta := range strings.Split(env["ANTHROPIC_BETAS"], ",") {
		if value := trim(beta); value != "" {
			betas = append(betas, value)
		}
	}
	if provider == "bedrock" {
		betas, _ = cloudauth.BedrockBetas(model, canonical, env, nil, false)
	}
	message := object(field("role", quoted("user")), field("content", json.RawMessage(`[{"type":"text","text":"Hi","cache_control":{"type":"ephemeral"}}]`)))
	history := []protocol.JSONObject{object(field("type", quoted("user")), field("message", rawObject(message)))}
	system := []protocol.JSONObject{}
	if !envFalse(env["CLAUDE_CODE_ATTRIBUTION_HEADER"]) {
		workload := ""
		if input.Host.Workload != "" {
			workload = " cc_workload=" + input.Host.Workload + ";"
		}
		system = append(system, object(field("type", quoted("text")), field("text", quoted("x-anthropic-billing-header: cc_version="+compatibilityVersion+"."+fingerprint(history)+"; cc_entrypoint=cli; cch=00000;"+workload))))
	}
	system = append(system, object(field("type", quoted("text")), field("text", quoted(cliPrefix))))
	metadataJSON, err := metadata(input)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	metadataValue := objectEnvironment(string(metadataJSON))
	max := float64(1)
	_, required, adaptive := nativeThinkingSupport(input)
	var thinking json.RawMessage
	if required && adaptive {
		_, upper := nativeModelLimits(model)
		max = 1 + 2048
		if max > upper {
			max = upper
		}
		thinking = json.RawMessage(`{"type":"adaptive"}`)
		if canonical == "claude-fable-5-1" {
			thinking = json.RawMessage(`{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`)
			betas = append(betas, "thinking-binding-controls-2026-08-01")
		}
	} else if envTrue(env["CC_HAHA_SEND_DISABLED_THINKING"]) {
		thinking = json.RawMessage(`{"type":"disabled"}`)
	}
	encode := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	params := object(field("model", quoted(regexp.MustCompile(`(?i)\[(1|2)m\]`).ReplaceAllString(model, ""))), field("max_tokens", rawNumber(max)), field("system", encode(system)), field("messages", encode([]protocol.JSONObject{message})), field("metadata", rawObject(metadataValue)))
	if len(betas) > 0 {
		params.Set("betas", encode(betas))
	}
	if thinking != nil {
		params.Set("thinking", thinking)
	}
	return params, nil
}
