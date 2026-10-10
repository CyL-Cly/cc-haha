package providerconfig

import (
	"encoding/json"
	"testing"
)

func TestDesktopModelMetadataDeduplicatesSlotsAndUsesPresetReasoningProfile(t *testing.T) {
	provider := saved("fixture")
	provider["presetId"] = encode("zhipuglm")
	provider["apiFormat"] = encode("anthropic")
	provider["models"] = json.RawMessage(`{"main":"glm-5.3","haiku":"glm-5.3","sonnet":"glm-5.2","opus":"glm-5.3","fable":"namespace/Kimi-K3"}`)
	list := ModelList(provider)
	if len(list) != 3 || str(list[0], "description") != "Main model" || str(list[0], "defaultReasoningEffort") != "max" || str(list[1], "description") != "Sonnet model" {
		t.Fatal("model slot priority/reasoning profile changed")
	}
	var efforts []string
	_ = json.Unmarshal(list[0]["supportedReasoningEfforts"], &efforts)
	if len(efforts) != 3 || efforts[0] != "low" || efforts[1] != "high" || efforts[2] != "max" {
		t.Fatal("standard API effort profile changed")
	}
}
