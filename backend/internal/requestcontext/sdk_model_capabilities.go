package requestcontext

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	weburl "github.com/nlnwa/whatwg-url/url"
	"strings"
)

// SDK initialization shares inference's real capability overrides. The built-in
// Claude list is trusted only for a first-party endpoint or authenticated Claude
// subscriber, as in modelSupportOverrides.ts.
func NativeSDKModelCapabilities(model string, env map[string]string, subscriber bool) (bool, []string, bool) {
	input := Context{Options: Options{Model: model}, Host: Host{Profile: NativeDirectRelayProfile, Env: env}}
	m := strings.ToLower(model)
	url, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
	firstParty := env["ANTHROPIC_BASE_URL"] == "" || err == nil && url.Host() == "api.anthropic.com"
	cloud := envTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || envTrue(env["CLAUDE_CODE_USE_VERTEX"]) || envTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) || envTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"])
	trust := !cloud && firstParty
	if firstParty && !cloud {
		input.Host.Env = map[string]string{}
		for k, v := range env {
			if !strings.HasSuffix(k, "_SUPPORTED_CAPABILITIES") {
				input.Host.Env[k] = v
			}
		}
	}
	supports := nativeSupportsEffort(input)
	if _, present := nativeCapability(input, "effort"); !present && !envTrue(env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"]) && !nativeOpenAIModel(model) {
		grok, _ := nativeGrokLevels(model)
		if !grok {
			supports = trust && (containsAny(m, "opus-4-6", "opus-4-7", "opus-5", "opus-4-8", "opus-4-5", "sonnet-4-6", "sonnet-5", "fable-5", "mythos-5", "mythos-preview")) || !cloud && (firstParty || subscriber) && !containsAny(m, "haiku", "sonnet", "opus")
		}
	}
	levels := []string{}
	if supports {
		levels = []string{"low", "medium", "high"}
		for _, level := range []string{"xhigh", "max"} {
			enabled := nativeSupportsEffortLevel(input, level)
			if nativeOpenAIModel(model) {
				if entry := providerproxy.OpenAIModelCatalogEntry(model, providerproxy.OpenAIModelCatalog()); entry != nil {
					enabled = false
					for _, supported := range entry.SupportedReasoningEfforts {
						enabled = enabled || supported == level
					}
				}
			}
			if _, present := nativeCapability(input, level+"_effort"); !present && !nativeOpenAIModel(model) {
				grok, _ := nativeGrokLevels(model)
				if !grok {
					parts := []string{"opus-4-7", "opus-5", "opus-4-8", "sonnet-5", "fable-5", "mythos-5"}
					if level == "max" {
						parts = append(parts, "opus-4-6", "sonnet-4-6", "mythos-preview")
					}
					enabled = trust && containsAny(m, parts...)
				}
			}
			if enabled {
				levels = append(levels, level)
			}
		}
	}
	_, _, adaptive := nativeThinkingSupport(input)
	if _, present := nativeCapability(input, "adaptive_thinking"); !present {
		canonical := nativeCanonicalModel(model)
		adaptive = nativeAdaptiveOnly(canonical)
		if !adaptive && ((!cloud && firstParty) || envTrue(env["CLAUDE_CODE_USE_FOUNDRY"])) {
			adaptive = containsAny(canonical, "sonnet-5", "opus-5", "opus-4-8", "opus-4-7", "opus-4-6", "sonnet-4-6") || !containsAny(canonical, "opus", "sonnet", "haiku")
		}
	}
	return supports, levels, adaptive
}
