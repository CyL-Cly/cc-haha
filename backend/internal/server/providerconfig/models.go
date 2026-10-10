package providerconfig

import (
	"slices"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

var effortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func EffortLevels() []string { return append([]string(nil), effortLevels...) }

// ModelList uses the same saved slots and preset capability override priority
// as the desktop Source API. It does not query an upstream catalog.
func ModelList(provider storage.Object) []storage.Object {
	models := object(provider["models"])
	format := str(provider, "apiFormat")
	list := []storage.Object{}
	for _, row := range []struct{ slot, description string }{{"main", "Main model"}, {"haiku", "Haiku model"}, {"sonnet", "Sonnet model"}, {"opus", "Opus model"}, {"fable", "Fable model"}} {
		id := str(models, row.slot)
		if strings.TrimSpace(id) == "" {
			continue
		}
		duplicate := false
		for _, model := range list {
			if str(model, "id") == id {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		info := storage.Object{"id": encode(id), "name": encode(id), "description": encode(row.description), "context": encode("")}
		if format != "" {
			profile := modelReasoning(provider, id)
			info["supportedReasoningEfforts"] = encode(profile.efforts)
			if profile.defaultEffort != "" {
				info["defaultReasoningEffort"] = encode(profile.defaultEffort)
			}
		}
		list = append(list, info)
	}
	return list
}

type reasoningProfile struct {
	efforts       []string
	defaultEffort string
}

// This profile is shared by the model-list API and explicit runtime selections.
func modelReasoning(provider storage.Object, id string) reasoningProfile {
	models := object(provider["models"])
	configured := preset(str(provider, "presetId"))
	defaults := object(configured["defaultEnv"])
	format := str(provider, "apiFormat")
	capabilities := capability(id, str(configured, "reasoningProviderKind"))
	overridden := false
	for _, slot := range []string{"fable", "opus", "sonnet", "haiku"} {
		mapped := str(models, slot)
		raw, exists := defaults["ANTHROPIC_DEFAULT_"+strings.ToUpper(slot)+"_MODEL_SUPPORTED_CAPABILITIES"]
		if exists && strings.EqualFold(strings.TrimSpace(mapped), strings.TrimSpace(id)) {
			capabilities = str(storage.Object{"value": raw}, "value")
			overridden = true
			break
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(id))
	normalized = strings.TrimSuffix(strings.TrimSuffix(normalized, "[1m]"), ":1m")
	if at := strings.LastIndex(normalized, "/"); at >= 0 {
		normalized = normalized[at+1:]
	}
	profile := reasoningProfile{efforts: EffortLevels()}
	if !overridden && format != "openai_responses" && (normalized == "glm-5.3" || normalized == "glm-5.3-flash") {
		profile.defaultEffort = "max"
	}
	if !overridden && format == "openai_responses" {
		capabilities = "thinking,effort,adaptive_thinking,xhigh_effort,max_effort"
	}
	if !overridden && !slices.Contains([]string{"", "anthropic", "openai_chat", "openai_responses"}, format) {
		capabilities = "none"
	}
	flags := strings.Split(strings.ToLower(capabilities), ",")
	for index := range flags {
		flags[index] = strings.TrimSpace(flags[index])
	}
	if !slices.Contains(flags, "effort") {
		profile.efforts = []string{}
	} else {
		profile.efforts = slices.DeleteFunc(profile.efforts, func(level string) bool {
			return level == "xhigh" && !slices.Contains(flags, "xhigh_effort") || level == "max" && !slices.Contains(flags, "max_effort")
		})
	}
	if !overridden && format != "openai_responses" && str(configured, "reasoningProviderKind") == "zhipu_standard_api" && (normalized == "glm-5.3" || normalized == "glm-5.3-flash") {
		profile.efforts = []string{"low", "high", "max"}
	}
	return profile
}
