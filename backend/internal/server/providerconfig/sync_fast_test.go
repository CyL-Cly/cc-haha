package providerconfig

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"testing"
)

func TestSyncOfficialFastSelectionAndEnvironmentDoNotLeakAcrossProviders(t *testing.T) {
	service, _ := fixture(t)
	seed(t, service.indexPath(), indexJSON(saved("fixture")))
	for _, id := range []string{"openai-official", "fixture"} {
		next, e := NormalizeRuntimeSelection(testContext, service.options, storage.Object{"providerId": encode(id), "modelId": encode("gpt-6-sol"), "openAIFastMode": encode(true)})
		if e != nil {
			t.Fatal(e)
		}
		if id == "openai-official" && (next.OpenAIFastMode == nil || !*next.OpenAIFastMode) {
			t.Fatal("official preference dropped")
		}
		if id == "fixture" && next.OpenAIFastMode != nil {
			t.Fatal("compatible provider accepted official priority")
		}
		env, e := service.ResolveRuntimeEnvironment(testContext, map[string]string{"CC_HAHA_OPENAI_FAST_MODE": "1", "USER_OWNED": "kept"}, &next.Selection)
		if e != nil {
			t.Fatal(e)
		}
		if (env["CC_HAHA_OPENAI_FAST_MODE"] == "1") != (id == "openai-official") || env["USER_OWNED"] != "kept" {
			t.Fatal(id, env)
		}
	}
}
