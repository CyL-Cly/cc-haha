package providerconfig

import (
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestRuntimeSelectionTrimsModelAndEffortAndUsesSharedProfile(t *testing.T) {
	service, _ := fixture(t)
	provider := saved("fixture")
	seed(t, service.indexPath(), indexJSON(provider))
	result, err := NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("fixture"), "modelId": encode(" deepseek-v4-flash "), "effortLevel": encode(" high ")})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID == nil || *result.ProviderID != "fixture" || result.ModelID != "deepseek-v4-flash" || result.EffortLevel != "high" || result.RequestedConfig != nil {
		t.Fatal("runtime selection was not normalized")
	}
	for _, input := range []storage.Object{{"modelId": encode(" ")}, {"modelId": encode(3)}, {"modelId": encode("model"), "effortLevel": encode("")}, {"modelId": encode("model"), "effortLevel": encode("ultra")}} {
		_, err := NormalizeRuntimeSelection(testContext, service.options, input)
		assertStatus(t, err, 400)
	}
	provider["presetId"] = encode("zhipuglm")
	provider["models"] = json.RawMessage(`{"main":"glm-5.3","haiku":"glm-5.3","sonnet":"glm-5.3","opus":"glm-5.3"}`)
	seed(t, service.indexPath(), indexJSON(provider))
	_, err = NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("fixture"), "modelId": encode("glm-5.3"), "effortLevel": encode("medium")})
	assertStatus(t, err, 400)
	result, err = NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("fixture"), "modelId": encode("glm-5.3"), "effortLevel": encode("high")})
	if err != nil || result.EffortLevel != "high" {
		t.Fatal("supported standard-API effort was lost")
	}
}

func TestDeletedRuntimeProviderFallsBackAsACompleteDefaultSelection(t *testing.T) {
	service, _ := fixture(t)
	provider := saved("active")
	seed(t, service.indexPath(), string(encode(storage.Object{"schemaVersion": encode(6), "providers": encode([]storage.Object{provider}), "activeId": encode("active"), "future": encode(true)})))
	if err := service.options.Settings.UpdateUserSettings(testContext, storage.Object{"model": encode("must-not-bleed"), "effort": encode("low"), "future": encode(true)}); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(testContext, "active"); err != nil {
		t.Fatal(err)
	}
	input := storage.Object{"providerId": encode("deleted"), "modelId": encode(" stale-model "), "effortLevel": encode(" high ")}
	result, err := NormalizeRuntimeSelection(testContext, service.options, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID == nil || *result.ProviderID != "active" || result.ModelID != "deepseek-v4-flash" || result.EffortLevel != "low" || str(result.RequestedConfig, "providerId") != "deleted" || str(result.RequestedConfig, "modelId") != "stale-model" || str(result.RequestedConfig, "effortLevel") != "high" {
		t.Fatal("deleted selection mixed stale and default configuration")
	}
	input["effortLevel"] = encode("invalid")
	_, err = NormalizeRuntimeSelection(testContext, service.options, input)
	assertStatus(t, err, 400)
	result, err = NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("openai-official"), "modelId": encode(" fixture ")})
	if err != nil || result.ProviderID == nil || *result.ProviderID != "openai-official" || result.ModelID != "fixture" || result.EffortLevel != "" {
		t.Fatalf("built-in OpenAI selection without effort unexpectedly queries a catalog: %v %v", result, err)
	}
	_, err = NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("openai-official"), "modelId": encode("fixture"), "effortLevel": encode("high")})
	assertStatus(t, err, 501)
	_, err = NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode("grok-official"), "modelId": encode("fixture")})
	assertStatus(t, err, 501)
}
