package providerconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDirectAnthropicAuthenticationMatchesFiveSourceStrategies(t *testing.T) {
	for _, row := range []struct {
		strategy string
		want     map[string]string
	}{{"api_key", map[string]string{"ANTHROPIC_API_KEY": "fake-private-key"}}, {"auth_token", map[string]string{"ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "fake-private-key"}}, {"auth_token_empty_api_key", map[string]string{"ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "fake-private-key"}}, {"dual_same_token", map[string]string{"ANTHROPIC_API_KEY": "fake-private-key", "ANTHROPIC_AUTH_TOKEN": "fake-private-key"}}, {"dual_dummy", map[string]string{"ANTHROPIC_API_KEY": "dummy", "ANTHROPIC_AUTH_TOKEN": "dummy"}}} {
		t.Run(row.strategy, func(t *testing.T) {
			provider := saved("fixture")
			provider["authStrategy"] = encode(row.strategy)
			env, err := BuildManagedEnv(provider)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
				if value, ok := env[key]; ok {
					got[key] = value
				}
			}
			if !reflect.DeepEqual(got, row.want) {
				t.Fatal("authentication projection changed")
			}
		})
	}
}
func TestManagedEnvModelCapabilitiesContextImageAndDefaultToken(t *testing.T) {
	provider := saved("fixture")
	provider["models"] = json.RawMessage(`{"main":" DeepSeek-v4-flash:1m ","haiku":"glm-5.3","sonnet":"namespace/Kimi-K3","opus":"custom-model","fable":"glm-5.2"}`)
	provider["model1mSupport"] = json.RawMessage(`{"main":true,"haiku":false,"sonnet":false,"opus":true}`)
	provider["toolSearchEnabled"] = encode(true)
	provider["disableExperimentalBetas"] = encode(true)
	provider["modelContextWindows"] = json.RawMessage(`{"z":200000,"2":300000,"a":16000}`)
	provider["autoCompactWindow"] = json.RawMessage(`1.6e4`)
	provider["requestCompatibility"] = json.RawMessage(`{"maxOutputTokens":4096}`)
	provider["imageGeneration"] = json.RawMessage(`{"model":" fixture-image "}`)
	env, err := BuildManagedEnv(provider)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"ANTHROPIC_MODEL": "DeepSeek-v4-flash[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL": "custom-model[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES": "thinking,required_thinking,effort,xhigh_effort,max_effort", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "thinking,required_thinking,effort,xhigh_effort,max_effort", "ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES": "thinking,effort,xhigh_effort,max_effort", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"2":300000,"deepseek-v4-pro[1m]":1000000,"deepseek-v4-pro":1000000,"deepseek-v4-flash":1000000,"deepseek-chat":1000000,"deepseek-reasoner":1000000,"z":200000,"a":16000}`, "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "16000", "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS": "4096", "ENABLE_TOOL_SEARCH": "true", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "CLAUDE_CODE_ATTRIBUTION_HEADER": "0", "CC_HAHA_IMAGE_PROVIDER_ID": "fixture", "CC_HAHA_IMAGE_MODEL": "fixture-image", "CC_HAHA_IMAGE_API_KEY": "fake-private-key", "CC_HAHA_IMAGE_BASE_URL": "http://127.0.0.1:1/anthropic"}
	for key, want := range expected {
		if env[key] != want {
			t.Fatalf("%s=%q want=%q", key, env[key], want)
		}
	}
	local := saved("local")
	local["presetId"] = encode("lmstudio")
	local["apiKey"] = encode("")
	local["apiFormat"] = encode("anthropic")
	localEnv, err := BuildManagedEnv(local)
	if err != nil {
		t.Fatal(err)
	}
	if localEnv["ANTHROPIC_AUTH_TOKEN"] != "lmstudio" || localEnv["ANTHROPIC_API_KEY"] != "" {
		t.Fatal("Source public preset default token changed")
	}
}
func TestUnsupportedOAuthAndProtocolProxyFailInsteadOfChangingRuntime(t *testing.T) {
	for _, patch := range []storage.Object{{"runtimeKind": encode("openai_oauth")}, {"runtimeKind": encode("grok_oauth")}, {"apiFormat": encode("openai_chat")}, {"apiFormat": encode("openai_responses")}, {"supportsNestedToolResultMedia": encode(false)}, {"presetId": encode("opencode-go"), "apiFormat": encode("anthropic")}} {
		provider := saved("fixture")
		for key, value := range patch {
			provider[key] = value
		}
		_, err := BuildManagedEnv(provider)
		assertStatus(t, err, 501)
	}
}
