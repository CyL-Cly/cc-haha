package providerconfig

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// BuildModelContextEnvironment is the non-secret model/window part of the
// actual provider environment producer, also read by transcript inspection.
func BuildModelContextEnvironment(provider storage.Object) (map[string]string, error) {
	models, valid := normalizedModels(provider["models"])
	if !valid {
		return nil, bad("Invalid provider model mapping")
	}
	p := preset(str(provider, "presetId"))
	env := map[string]string{}
	defaults := object(p["defaultEnv"])
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"} {
		if raw := defaults[key]; len(raw) > 0 {
			var value string
			if json.Unmarshal(raw, &value) == nil {
				env[key] = value
			}
		}
	}
	windows := protocol.NewJSONObject()
	var fromPreset, fromSaved protocol.JSONObject
	if json.Unmarshal(p["modelContextWindows"], &fromPreset) == nil {
		windows.Assign(fromPreset)
	}
	if json.Unmarshal(provider["modelContextWindows"], &fromSaved) == nil {
		windows.Assign(fromSaved)
	}
	if raw, err := windows.MarshalJSON(); err == nil && string(raw) != "{}" {
		env["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = string(protocol.StringifyValidatedJavaScriptJSON(raw))
	}
	oneM := object(provider["model1mSupport"])
	env["ANTHROPIC_MODEL"] = oneMillion(str(models, "main"), string(oneM["main"]) == "true")
	for _, slot := range []string{"fable", "haiku", "sonnet", "opus"} {
		if model := str(models, slot); model != "" {
			env["ANTHROPIC_DEFAULT_"+strings.ToUpper(slot)+"_MODEL"] = oneMillion(model, string(oneM[slot]) == "true")
		}
	}
	return env, nil
}
