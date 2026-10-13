package providerconfig

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// RequestedConfig is populated only when a deleted provider caused Source's
// replacement with the complete current default. It lets the desktop confirm
// both the original request and the configuration actually applied.
type RuntimeSelection struct {
	Selection
	RequestedConfig storage.Object
}

func invalidSelection(reason string) error {
	return apiError(400, "RUNTIME_CONFIG_INVALID", "Runtime "+reason+" selection is invalid.")
}

// NormalizeRuntimeSelection ports normalizeRuntimeOverrideInput. It does not
// contact model catalogs or accept an OAuth provider under an invented profile.
// input retains the distinction between omitted effort and an empty string.
func NormalizeRuntimeSelection(ctx context.Context, options Options, input storage.Object) (RuntimeSelection, error) {
	model := protocol.TrimJavaScriptString(str(input, "modelId"))
	if model == "" {
		return RuntimeSelection{}, invalidSelection("model")
	}
	var requestedEffort *string
	if raw, exists := input["effortLevel"]; exists && string(raw) != "null" {
		var effort string
		if json.Unmarshal(raw, &effort) == nil {
			effort = protocol.TrimJavaScriptString(effort)
			requestedEffort = &effort
		}
	}
	service, err := New(options)
	if err != nil {
		return RuntimeSelection{}, err
	}
	index, err := service.ReadIndex(ctx)
	if err != nil {
		return RuntimeSelection{}, err
	}
	var id *string
	if raw := input["providerId"]; len(raw) > 0 && string(raw) != "null" {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			id = &value
		}
	}
	var provider storage.Object
	if id != nil {
		if *id == "openai-official" {
			selection := RuntimeSelection{Selection: Selection{ProviderID: id, ModelID: model}}
			var fast bool
			if json.Unmarshal(input["openAIFastMode"], &fast) == nil && string(input["openAIFastMode"]) != "null" {
				selection.OpenAIFastMode = &fast
			}
			if requestedEffort == nil {
				return selection, nil
			}
			if !slices.Contains(EffortLevels(), *requestedEffort) {
				return RuntimeSelection{}, invalidSelection("effort")
			}
			if options.OpenAIModelCatalog == nil {
				return RuntimeSelection{}, unavailable("Go desktop OpenAI model catalog consumer is unavailable")
			}
			catalog, err := options.OpenAIModelCatalog(ctx)
			if err != nil {
				return RuntimeSelection{}, err
			}
			entry := providerproxy.OpenAIModelCatalogEntry(model, catalog)
			if entry != nil && !slices.Contains(entry.SupportedReasoningEfforts, *requestedEffort) {
				return RuntimeSelection{}, invalidSelection("effort")
			}
			selection.EffortLevel = *requestedEffort
			return selection, nil
		}
		if *id == "grok-official" {
			return normalizeGrokSelection(ctx, options, id, model, requestedEffort)
		}
		for _, entry := range providers(index) {
			if str(entry, "id") == *id {
				provider = entry
				break
			}
		}
		if provider == nil {
			if requestedEffort != nil && !slices.Contains(EffortLevels(), *requestedEffort) {
				return RuntimeSelection{}, invalidSelection("effort")
			}
			defaults, err := defaultRuntimeSelection(ctx, service, index)
			if err != nil {
				return RuntimeSelection{}, err
			}
			defaults.RequestedConfig = storage.Object{"providerId": encode(*id), "modelId": encode(model)}
			if requestedEffort != nil {
				defaults.RequestedConfig["effortLevel"] = encode(*requestedEffort)
			}
			var fast bool
			if json.Unmarshal(input["openAIFastMode"], &fast) == nil && string(input["openAIFastMode"]) != "null" {
				defaults.RequestedConfig["openAIFastMode"] = encode(fast)
			}
			return defaults, nil
		}
	}
	selection := RuntimeSelection{Selection: Selection{ProviderID: id, ModelID: model}}
	if requestedEffort == nil {
		return selection, nil
	}
	if !slices.Contains(EffortLevels(), *requestedEffort) {
		return RuntimeSelection{}, invalidSelection("effort")
	}
	if provider == nil {
		selection.EffortLevel = *requestedEffort
		return selection, nil
	}
	profile := modelReasoning(provider, model)
	if slices.Contains(profile.efforts, *requestedEffort) {
		selection.EffortLevel = *requestedEffort
	} else if str(preset(str(provider, "presetId")), "reasoningProviderKind") == "zhipu_standard_api" {
		return RuntimeSelection{}, invalidSelection("effort")
	}
	return selection, nil
}

func defaultRuntimeSelection(ctx context.Context, service *Service, index storage.Object) (RuntimeSelection, error) {
	selection, err := defaultRuntimeSettings(ctx, service, index)
	if err != nil {
		return selection, err
	}
	if selection.ModelID == "" {
		selection.ModelID = str(object(object(index["officialProviderModels"])["claude-official"]), "main")
		if selection.ModelID == "" {
			selection.ModelID = "claude-opus-5-5"
		}
	}
	return selection, nil
}

// ResolveDesktopRuntimeSelection runs Source's initial/default settings and
// restored null-provider subscription resolver. The ordinary CLI never binds it.
func ResolveDesktopRuntimeSelection(ctx context.Context, options Options, restored *Selection) (Selection, error) {
	service, err := New(options)
	if err != nil {
		return Selection{}, err
	}
	index, err := service.ReadIndex(ctx)
	if err != nil {
		return Selection{}, err
	}
	if restored == nil {
		selection, err := defaultRuntimeSettings(ctx, service, index)
		return selection.Selection, err
	}
	selection := *restored
	if selection.ProviderID == nil {
		if options.ClaudeRuntimeModel == nil {
			return Selection{}, unavailable("Default runtime selection requires the desktop Claude OAuth producer")
		}
		model, err := options.ClaudeRuntimeModel(ctx, encode(selection.ModelID))
		if err != nil {
			return Selection{}, err
		}
		if model != nil {
			selection.ModelID = *model
		}
	}
	return selection, nil
}

func defaultRuntimeSettings(ctx context.Context, service *Service, index storage.Object) (RuntimeSelection, error) {
	if service.options.Settings == nil {
		return RuntimeSelection{}, unavailable("Default runtime selection requires shared user settings")
	}
	id := str(index, "activeId")
	var provider storage.Object
	if id == "grok-official" {
		provider = grokOfficialProvider(index)
	}
	if id != "" {
		if id == "openai-official" {
			provider = openAIOfficialProvider(index)
		}
		if provider == nil {
			for _, entry := range providers(index) {
				if str(entry, "id") == id {
					provider = entry
					break
				}
			}
		}
		if provider == nil {
			if err := service.ActivateOfficial(ctx); err != nil {
				return RuntimeSelection{}, err
			}
			id = ""
		}
	}
	user, err := service.options.Settings.GetUserSettings(ctx)
	if err != nil {
		return RuntimeSelection{}, err
	}
	modelSettings := user
	if id != "" {
		if service.options.Managed == nil {
			return RuntimeSelection{}, unavailable("Default runtime selection requires shared managed settings")
		}
		modelSettings, err = service.options.Managed.ReadSettings(ctx)
		if err != nil {
			return RuntimeSelection{}, err
		}
	}
	model := str(modelSettings, "model")
	if protocol.TrimJavaScriptString(model) == "" {
		model = ""
	} else if tier := str(modelSettings, "modelContext"); protocol.TrimJavaScriptString(tier) != "" {
		model += ":" + tier
	}
	if model == "" {
		if provider != nil {
			env, err := service.buildManagedEnv(provider)
			if err != nil {
				return RuntimeSelection{}, err
			}
			model = env["ANTHROPIC_MODEL"]
		}
	}
	if id == "" {
		if service.options.ClaudeRuntimeModel == nil {
			return RuntimeSelection{}, unavailable("Default runtime selection requires the desktop Claude OAuth producer")
		}
		var configured json.RawMessage
		if protocol.TrimJavaScriptString(str(user, "model")) != "" {
			configured = user["model"]
		}
		resolved, err := service.options.ClaudeRuntimeModel(ctx, configured)
		if err != nil {
			return RuntimeSelection{}, err
		}
		if resolved != nil {
			model = *resolved
		}
	}
	selection := RuntimeSelection{Selection: Selection{ModelID: model}}
	if id != "" {
		selection.ProviderID = &id
	}
	if effort := str(user, "effort"); protocol.TrimJavaScriptString(effort) != "" {
		selection.EffortLevel = effort
	}
	if id == "openai-official" {
		if protocol.TrimJavaScriptString(str(modelSettings, "model")) == "" {
			selection.ModelID = providerproxy.OpenAIDefaultMainModel
		}
		if service.options.OpenAIModelCatalog == nil {
			return RuntimeSelection{}, unavailable("Go desktop OpenAI model catalog consumer is unavailable")
		}
		catalog, err := service.options.OpenAIModelCatalog(ctx)
		if err != nil {
			return RuntimeSelection{}, err
		}
		selection.EffortLevel = "medium"
		if entry := providerproxy.OpenAIModelCatalogEntry(selection.ModelID, catalog); entry != nil {
			selection.EffortLevel = entry.DefaultReasoningEffort
		}
		return selection, nil
	}
	if id == "grok-official" {
		if protocol.TrimJavaScriptString(str(modelSettings, "model")) == "" {
			selection.ModelID = providerproxy.GrokDefaultMainModel
		}
		entry, err := grokRuntimeEntry(ctx, service.options, selection.ModelID)
		if err != nil {
			return RuntimeSelection{}, err
		}
		selection.EffortLevel = entry.ReasoningEffort
		return selection, nil
	}
	if provider != nil {
		profile := modelReasoning(provider, model)
		if !slices.Contains(profile.efforts, selection.EffortLevel) {
			if profile.defaultEffort != "" {
				selection.EffortLevel = profile.defaultEffort
			}
		}
	}
	return selection, nil
}
