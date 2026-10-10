package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	weburl "github.com/nlnwa/whatwg-url/url"
	"strings"
)

func nativeSDKModelOptions(env map[string]string, settings protocol.JSONObject, global *protocol.JSONObject, selector *modelselection.Selector, initial *string, identity modelselection.MetadataIdentity, openAI ...bool) ([]tui.ModelOption, error) {
	if env["USER_TYPE"] == "ant" {
		return nil, mainCap("SDK internal Ant model catalog producer")
	}
	url, err := weburl.Parse(env["ANTHROPIC_BASE_URL"])
	third := !identity.Subscriber && (env["ANTHROPIC_BASE_URL"] != "" && (err != nil || url.Host() != "api.anthropic.com") || pickerTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || pickerTrue(env["CLAUDE_CODE_USE_VERTEX"]) || pickerTrue(env["CLAUDE_CODE_USE_FOUNDRY"]))
	cloud := pickerTrue(env["CLAUDE_CODE_USE_BEDROCK"]) || pickerTrue(env["CLAUDE_CODE_USE_VERTEX"]) || pickerTrue(env["CLAUDE_CODE_USE_FOUNDRY"]) || pickerTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"])
	if len(openAI) > 0 && openAI[0] && !cloud {
		resolved, e := nativePickerResolve(selector, nil)
		if e != nil {
			return nil, e
		}
		label, e := nativePickerMarketing(selector, resolved)
		if e != nil {
			return nil, e
		}
		if label == "" {
			label = resolved
		}
		description := "Use the default model (currently " + label + ")"
		if len(openAI) > 1 && openAI[1] {
			description = "Use the OpenAI/Codex default model (currently " + label + ")"
		} else if !third {
			priceModel := "claude-sonnet-5-5"
			if strings.Contains(label, "Opus") {
				priceModel = "claude-opus-5-5"
			}
			price, _ := modelaccounting.NativeModelPricingString(priceModel)
			description += " · " + price
		}
		base := []tui.ModelOption{{Label: "Default (recommended)", Description: description}}
		for _, entry := range providerproxy.OpenAIModelCatalog() {
			base = append(base, tui.ModelOption{Value: pickerPointer(entry.Value), Label: entry.Label, Description: entry.Description})
		}
		if third {
			custom, e := nativePickerOptionsWithBase(env, settings, global, selector, nil, nil)
			if e != nil {
				return nil, e
			}
			for _, tier := range []string{"sonnet", "fable", "opus", "haiku"} {
				if env["ANTHROPIC_DEFAULT_"+strings.ToUpper(tier)+"_MODEL"] != "" {
					for _, option := range custom {
						if option.Value != nil && *option.Value == tier {
							base = append(base, option)
							break
						}
					}
				}
			}
		}
		return nativePickerOptionsWithBase(env, settings, global, selector, initial, base)
	}
	if third {
		return nativePickerOptionsWithBase(env, settings, global, selector, initial, nil)
	}
	resolved, err := nativePickerResolve(selector, nil)
	if err != nil {
		return nil, err
	}
	label, err := nativePickerMarketing(selector, resolved)
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = resolved
	}
	price := func(model string) string { v, _ := modelaccounting.NativeModelPricingString(model); return v }
	premium := identity.SubscriptionType == "max" || identity.SubscriptionType == "team" && identity.RateLimitTier == "default_claude_max_5x"
	merge := identity.Subscriber && identity.SubscriptionType != "" && identity.SubscriptionType != "pro" && !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	description := "Use the default model (currently " + label + ") · " + price("claude-sonnet-5-5")
	if strings.Contains(label, "Opus") {
		description = "Use the default model (currently " + label + ") · " + price("claude-opus-5-5")
	}
	if identity.Subscriber {
		description = "Sonnet 5.5 · Best for everyday tasks"
		if premium {
			description = "Opus 5.5 · Most capable for complex work"
			if merge {
				description = "Opus 5.5 with 1M context · Most capable for complex work"
			}
		}
	}
	base := []tui.ModelOption{{Label: "Default (recommended)", Description: description}}
	if pickerTrue(env["CLAUDE_CODE_USE_AZURE_OPENAI"]) {
		base[0].Description = "Use the default model (currently " + label + ")"
		for _, m := range []string{"gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4-codex"} {
			base = append(base, tui.ModelOption{Value: pickerPointer(m), Label: strings.ToUpper(m[:3]) + m[3:len(m)-6] + " Codex", Description: "Azure OpenAI Codex model"})
		}
		return nativePickerOptionsWithBase(env, settings, global, selector, initial, base)
	}
	base = append(base, tui.ModelOption{Value: pickerPointer("fable"), Label: "Fable", Description: "Fable 5 · Highest capability for long-running tasks · " + price("claude-fable-5")})
	extra := global != nil && (string(global.Get("cachedExtraUsageDisabledReason")) == "null" || global.String("cachedExtraUsageDisabledReason") == "out_of_credits")
	access := !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (!identity.Subscriber || extra)
	sonnet, err := nativePickerResolve(selector, pickerPointer("sonnet"))
	if err != nil {
		return nil, err
	}
	offerSonnet1m := access && !strings.Contains(strings.ToLower(sonnet), "sonnet-5")
	appendSonnet1m := func(subscriber bool) {
		if !offerSonnet1m {
			return
		}
		description := "Sonnet 5.5 for long sessions"
		if subscriber {
			description = "Sonnet 5.5 with 1M context · Billed as extra usage"
		}
		base = append(base, tui.ModelOption{Value: pickerPointer("sonnet[1m]"), Label: "Sonnet (1M context)", Description: description + " · " + price("claude-sonnet-5-5")})
	}
	if identity.Subscriber {
		if premium {
			if !merge && access {
				base = append(base, tui.ModelOption{Value: pickerPointer("opus[1m]"), Label: "Opus (1M context)", Description: "Opus 5.5 with 1M context · Billed as extra usage · " + price("claude-opus-5-5")})
			}
			base = append(base, tui.ModelOption{Value: pickerPointer("sonnet"), Label: "Sonnet", Description: "Sonnet 5.5 · Best for everyday tasks"})
			appendSonnet1m(true)
		} else {
			appendSonnet1m(true)
			if merge {
				base = append(base, tui.ModelOption{Value: pickerPointer("opus[1m]"), Label: "Opus (1M context)", Description: "Opus 5.5 with 1M context · Most capable for complex work"})
			} else {
				base = append(base, tui.ModelOption{Value: pickerPointer("opus"), Label: "Opus", Description: "Opus 5.5 · Most capable for complex work"})
				if access {
					base = append(base, tui.ModelOption{Value: pickerPointer("opus[1m]"), Label: "Opus (1M context)", Description: "Opus 5.5 with 1M context · Billed as extra usage · " + price("claude-opus-5-5")})
				}
			}
		}
	} else {
		base = append(base, tui.ModelOption{Value: pickerPointer("sonnet"), Label: "Sonnet", Description: "Sonnet 5.5 · Best for everyday tasks · " + price("claude-sonnet-5-5")})
		appendSonnet1m(false)
	}
	haiku := "Haiku 4.5 · Fastest for quick answers"
	if !identity.Subscriber {
		haiku += " · " + price("claude-haiku-4-5")
	}
	base = append(base, tui.ModelOption{Value: pickerPointer("haiku"), Label: "Haiku", Description: haiku})
	return nativePickerOptionsWithBase(env, settings, global, selector, initial, base)
}

func nativeSDKOpusPriceSuffix(env map[string]string, price string) string {
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if pickerTrue(env[key]) {
			return ""
		}
	}
	return " · " + price
}
