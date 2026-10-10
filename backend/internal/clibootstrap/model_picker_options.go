package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// nativePickerOptions ports Source's external direct credential PAYG 3P menu.
// Subscription identity, provider-discovered model catalogs and fast-mode
// billing need their original backing state before those branches can run.
func nativePickerOptions(env map[string]string, settings protocol.JSONObject, global *protocol.JSONObject, selector *modelselection.Selector, initial *string) ([]tui.ModelOption, error) {
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if pickerTrue(env[key]) {
			return nil, mainCap("model picker provider catalog " + key)
		}
	}
	url, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
	if env["ANTHROPIC_BASE_URL"] == "" || err == nil && url.Host() == "api.anthropic.com" {
		return nil, mainCap("model picker first-party Claude effort and fast-mode state")
	}
	return nativePickerOptionsWithBase(env, settings, global, selector, initial, nil)
}

func nativePickerOptionsWithBase(env map[string]string, settings protocol.JSONObject, global *protocol.JSONObject, selector *modelselection.Selector, initial *string, base []tui.ModelOption) ([]tui.ModelOption, error) {
	defaultModel, err := nativePickerResolve(selector, nil)
	if err != nil {
		return nil, err
	}
	defaultLabel, err := nativePickerMarketing(selector, defaultModel)
	if err != nil {
		return nil, err
	}
	if defaultLabel == "" {
		defaultLabel = defaultModel
	}
	options := []tui.ModelOption{{Label: "Default (recommended)", Description: "Use the default model (currently " + defaultLabel + ")"}}
	custom := func(tier string) *tui.ModelOption {
		prefix := "ANTHROPIC_DEFAULT_" + strings.ToUpper(tier) + "_MODEL"
		model := env[prefix]
		if model == "" {
			return nil
		}
		label := model
		if value, exists := env[prefix+"_NAME"]; exists {
			label = value
		}
		description := "Custom " + strings.ToUpper(tier[:1]) + tier[1:] + " model"
		if tier != "haiku" && !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (strings.Contains(strings.ToLower(model), "[1m]") || strings.HasSuffix(strings.ToLower(model), ":1m")) {
			description += " (1M context)"
		}
		if value, exists := env[prefix+"_DESCRIPTION"]; exists {
			description = value
		}
		return &tui.ModelOption{Value: pickerPointer(tier), Label: label, Description: description}
	}
	push := func(option tui.ModelOption) {
		for _, old := range options {
			if pickerSame(old.Value, option.Value) {
				return
			}
		}
		options = append(options, option)
	}
	modelID := func(canonical string) string {
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON(settings.Get("modelOverrides")) == nil {
			if raw := overrides.Get(canonical); raw != nil {
				if value, err := decodeNativeModel(raw); err == nil && value != "" {
					return value
				}
			}
		}
		return selector.SDKCatalogModelID(canonical)
	}
	if base == nil {
		if option := custom("fable"); option != nil {
			push(*option)
		}
		if option := custom("sonnet"); option != nil {
			push(*option)
		} else {
			push(tui.ModelOption{Value: pickerPointer(modelID("claude-sonnet-4-6")), Label: "Sonnet", Description: "Sonnet 4.6 · Best for everyday tasks"})
			if !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
				push(tui.ModelOption{Value: pickerPointer(modelID("claude-sonnet-4-6") + "[1m]"), Label: "Sonnet (1M context)", Description: "Sonnet 4.6 for long sessions"})
			}
		}
		if option := custom("opus"); option != nil {
			push(*option)
		} else {
			push(tui.ModelOption{Value: pickerPointer("opus"), Label: "Opus 4.1", Description: "Opus 4.1 · Legacy"})
			// This branch's default Opus is exactly the Source 4.7 tier. A custom
			// Opus env override is handled above and has its own original text.
			price, found := modelaccounting.NativeModelPricingString("claude-opus-4-7")
			if !found {
				return nil, mainCap("model picker shared Opus pricing")
			}
			push(tui.ModelOption{Value: pickerPointer(modelID("claude-opus-4-7")), Label: "Opus", Description: "Opus 4.7 · Most capable for complex work" + nativeSDKOpusPriceSuffix(env, price)})
			if !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
				push(tui.ModelOption{Value: pickerPointer(modelID("claude-opus-4-7") + "[1m]"), Label: "Opus (1M context)", Description: "Opus 4.7 for long sessions" + nativeSDKOpusPriceSuffix(env, price)})
			}
		}
		if option := custom("haiku"); option != nil {
			push(*option)
		} else {
			push(tui.ModelOption{Value: pickerPointer("haiku"), Label: "Haiku", Description: "Haiku 4.5 · Fastest for quick answers"})
		}
	} else {
		options = base
	}
	if model := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; model != "" {
		label := model
		if value, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"]; ok {
			label = value
		}
		description := "Custom model (" + model + ")"
		if value, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION"]; ok {
			description = value
		}
		push(tui.ModelOption{Value: pickerPointer(model), Label: label, Description: description})
	}
	if global != nil && global.Get("additionalModelOptionsCache") != nil {
		var cached []protocol.JSONObject
		if err := json.Unmarshal(global.Get("additionalModelOptionsCache"), &cached); err != nil {
			return nil, mainCap("model picker original additional options array")
		}
		for _, raw := range cached {
			var value *string
			if raw.Get("value") == nil {
				return nil, mainCap("model picker cached undefined value")
			}
			if string(raw.Get("value")) != "null" {
				v, err := decodeNativeModel(raw.Get("value"))
				if err != nil {
					return nil, err
				}
				value = pickerPointer(v)
			}
			label, err := decodeNativeModel(raw.Get("label"))
			if err != nil {
				return nil, err
			}
			description, err := decodeNativeModel(raw.Get("description"))
			if err != nil {
				return nil, err
			}
			push(tui.ModelOption{Value: value, Label: label, Description: description})
		}
	}
	current, err := selector.UserSpecified()
	if err != nil {
		return nil, err
	}
	customModel := pickerClone(initial)
	if current != nil && string(current) != "null" {
		v, err := decodeNativeModel(current)
		if err != nil {
			return nil, err
		}
		customModel = pickerPointer(v)
	}
	if customModel != nil {
		found := false
		for _, option := range options {
			found = found || pickerSame(option.Value, customModel)
		}
		if !found {
			switch *customModel {
			case "opus":
				if base != nil {
					options = append(options, tui.ModelOption{Value: pickerPointer("opus"), Label: "Opus", Description: "Opus 5.5 · Most capable for complex work"})
					break
				}
				label, e := nativePickerMarketing(selector, *customModel)
				if e != nil {
					return nil, e
				}
				if label == "" {
					label = *customModel
				}
				push(tui.ModelOption{Value: pickerClone(customModel), Label: label, Description: "Custom model"})
			case "opusplan":
				push(tui.ModelOption{Value: pickerClone(customModel), Label: "Opus Plan Mode", Description: "Use Opus in plan mode, Sonnet otherwise"})
			case "opus[1m]":
				if base != nil {
					options = append(options, tui.ModelOption{Value: pickerPointer("opus[1m]"), Label: "Opus (1M context)", Description: "Opus 5.5 with 1M context · Most capable for complex work"})
					break
				}
				options = append(options, tui.ModelOption{Value: pickerPointer(modelID("claude-opus-4-7") + "[1m]"), Label: "Opus (1M context)", Description: "Opus 4.7 with 1M context · Most capable for complex work"})
			default:
				label, err := nativePickerMarketing(selector, *customModel)
				if err != nil {
					return nil, err
				}
				description := "Custom model"
				if label != "" {
					description = *customModel
					canonicalRaw, err := selector.Canonical(quoteMain(*customModel))
					if err != nil {
						return nil, err
					}
					canonical, err := decodeNativeModel(canonicalRaw)
					if err != nil {
						return nil, err
					}
					tier := ""
					switch {
					case strings.Contains(canonical, "claude-fable-5"):
						tier = "fable"
					case strings.Contains(canonical, "claude-sonnet-5") || strings.Contains(canonical, "claude-sonnet-4-6") || strings.Contains(canonical, "claude-sonnet-4-5") || strings.Contains(canonical, "claude-sonnet-4-") || strings.Contains(canonical, "claude-3-7-sonnet") || strings.Contains(canonical, "claude-3-5-sonnet"):
						tier = "sonnet"
					case strings.Contains(canonical, "claude-opus-4"):
						tier = "opus"
					case strings.Contains(canonical, "claude-haiku") || strings.Contains(canonical, "claude-3-5-haiku"):
						tier = "haiku"
					}
					if tier != "" {
						resolved, err := nativePickerResolve(selector, pickerPointer(tier))
						if err != nil {
							return nil, err
						}
						latest, err := nativePickerMarketing(selector, resolved)
						if err != nil {
							return nil, err
						}
						defaultCanonicalRaw, err := selector.Canonical(quoteMain(resolved))
						if err != nil {
							return nil, err
						}
						defaultCanonical, err := decodeNativeModel(defaultCanonicalRaw)
						if err != nil {
							return nil, err
						}
						pinnedNewerFable := canonical == "claude-fable-5-1" && defaultCanonical == "claude-fable-5"
						if latest != "" && latest != label && !pinnedNewerFable {
							description = "Newer version available · select " + strings.ToUpper(tier[:1]) + tier[1:] + " for " + latest
						}
					}
				} else {
					label = *customModel
				}
				push(tui.ModelOption{Value: pickerClone(customModel), Label: label, Description: description})
			}
		}
	}
	filtered := make([]tui.ModelOption, 0, len(options))
	for _, option := range options {
		if option.Value == nil {
			filtered = append(filtered, option)
			continue
		}
		allowed, err := selector.Allowed(quoteMain(*option.Value))
		if err != nil {
			return nil, err
		}
		if allowed {
			filtered = append(filtered, option)
		}
	}
	return filtered, nil
}
