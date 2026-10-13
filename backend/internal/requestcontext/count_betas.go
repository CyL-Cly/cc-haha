package requestcontext

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// DirectCountingBetas is getModelBetas for the direct external client. It uses
// the same capability and canonical-name producers as ordinary native requests.
// Subscription and experiment identities remain with their actual host.
func DirectCountingBetas(model string, env map[string]string, settings protocol.JSONObject) ([]string, error) {
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI", "CLAUDE_CODE_REMOTE"} {
		if envTrue(env[name]) {
			return nil, unsupported("token counting provider " + name)
		}
	}
	if env["USER_TYPE"] == "ant" {
		return nil, unsupported("internal token counting beta experiments")
	}
	canonicalInput := model
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON(settings.Get("modelOverrides")) == nil && overrides.IsObject() {
		for _, field := range overrides.OwnProperties() {
			var value, key string
			_ = json.Unmarshal(field.Value, &value)
			if value == model {
				_ = json.Unmarshal(field.NameJSON, &key)
				canonicalInput = key
				break
			}
		}
	}
	canonical := nativeCanonicalModel(canonicalInput)
	trusted := env["ANTHROPIC_BASE_URL"] == ""
	if !trusted {
		u, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
		trusted = err == nil && u.Host() == "api.anthropic.com"
	}
	firstParty := trusted && !envTrue(env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"])
	betas := []string{}
	if !strings.Contains(canonical, "haiku") {
		betas = append(betas, "claude-code-20250219")
	}
	if !envTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (strings.Contains(strings.ToLower(model), "[1m]") || strings.HasSuffix(strings.ToLower(model), ":1m")) {
		betas = append(betas, "context-1m-2025-08-07")
	}
	isp, overridden := nativeCapability(Context{Options: Options{Model: model}, Host: Host{Env: env}}, "interleaved_thinking")
	if trusted {
		overridden = false
	}
	if !overridden {
		isp = trusted && !strings.Contains(canonical, "claude-3-")
	}
	if isp && !envTrue(env["DISABLE_INTERLEAVED_THINKING"]) {
		betas = append(betas, "interleaved-thinking-2025-05-14")
	}
	if firstParty && !strings.Contains(canonical, "claude-3-") {
		betas = append(betas, "context-management-2025-06-27")
	}
	if firstParty {
		betas = append(betas, "prompt-caching-scope-2026-01-05")
	}
	for _, raw := range strings.Split(env["ANTHROPIC_BETAS"], ",") {
		if value := trim(raw); value != "" {
			// getModelBetas preserves explicit duplicates and order.
			betas = append(betas, value)
		}
	}
	return betas, nil
}
