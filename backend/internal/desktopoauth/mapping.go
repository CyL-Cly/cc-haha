package desktopoauth

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

// ModelMapping is the unchanged desktop provider model-alias shape. Optional
// Fable is omitted after normalization when its trimmed value is empty.
type ModelMapping = settings.ModelMapping

func BuiltInProvider(id string) bool {
	return id == "claude-official" || id == "openai-official" || id == "grok-official"
}

func DefaultProviderModels(id string) ModelMapping {
	switch id {
	case "openai-official":
		return ModelMapping{Main: "gpt-6-sol", Haiku: "gpt-6-luna", Sonnet: "gpt-6-sol", Opus: "gpt-6-astra"}
	case "grok-official":
		return ModelMapping{Main: "grok-4.7", Haiku: "grok-4.7", Sonnet: "grok-4.7", Opus: "grok-4.7"}
	default:
		return ModelMapping{Main: "claude-opus-5-5", Fable: "claude-fable-5-1", Haiku: "claude-haiku-4-5", Sonnet: "claude-sonnet-5-5", Opus: "claude-opus-5-5"}
	}
}

func trimJS(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\uFEFF' || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Zs, r)
	})
}

func NormalizeProviderModels(value ModelMapping) ModelMapping {
	value.Main = trimJS(value.Main)
	value.Fable = trimJS(value.Fable)
	value.Haiku = trimJS(value.Haiku)
	value.Sonnet = trimJS(value.Sonnet)
	value.Opus = trimJS(value.Opus)
	if value.Haiku == "" {
		value.Haiku = value.Main
	}
	if value.Sonnet == "" {
		value.Sonnet = value.Main
	}
	if value.Opus == "" {
		value.Opus = value.Main
	}
	return value
}

func EncodeProviderModels(value ModelMapping) json.RawMessage {
	result := record()
	result.Set("main", encode(value.Main))
	if value.Fable != "" {
		result.Set("fable", encode(value.Fable))
	}
	result.Set("haiku", encode(value.Haiku))
	result.Set("sonnet", encode(value.Sonnet))
	result.Set("opus", encode(value.Opus))
	return encode(result)
}

// OfficialManagedEnvironment ports the original built-in OAuth mapping env.
// grokWindows contains the actual runtime catalog, merged after bundled rows
// like getGrokRuntimeModelCatalog. The OAuth runtime owns that live catalog.
func OfficialManagedEnvironment(id, configDir string, value ModelMapping, grokWindows *protocol.JSONObject) (protocol.JSONObject, error) {
	env := record()
	windows := record()
	switch id {
	case "openai-official":
		env.Set("CC_HAHA_OPENAI_OAUTH_PROVIDER", encode("1"))
		env.Set("OPENAI_CODEX_OAUTH_FILE", encode(filepath.Join(configDir, "cc-haha", "openai-oauth.json")))
		env.Set("CC_HAHA_IMAGE_PROVIDER_KIND", encode("openai_oauth"))
		env.Set("CC_HAHA_IMAGE_PROVIDER_ID", encode(id))
		env.Set("CC_HAHA_IMAGE_MODEL", encode("gpt-image-2"))
		for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.3-codex", "gpt-5.4", "gpt-5.5", "gpt-5.4-mini"} {
			context := 258400
			switch model {
			case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
				context = 353400
			case "gpt-5.4":
				context = 950000
			}
			windows.Set(model, encode(context))
		}
	case "grok-official":
		env.Set("CC_HAHA_GROK_OAUTH_PROVIDER", encode("1"))
		env.Set("GROK_OAUTH_FILE", encode(filepath.Join(configDir, "cc-haha", "grok-oauth.json")))
		env.Set("CC_HAHA_IMAGE_PROVIDER_KIND", encode("grok_oauth"))
		env.Set("CC_HAHA_IMAGE_PROVIDER_ID", encode(id))
		env.Set("CC_HAHA_IMAGE_MODEL", encode("grok-imagine-image-quality"))
		for _, model := range []string{"grok-4.7", "grok-4.7-build-fast", "grok-4.6", "grok-4.5"} {
			windows.Set(model, encode(500000))
		}
		if grokWindows != nil {
			windows.Assign(*grokWindows)
		}
	default:
		return env, errors.New("Official managed environment requires an OpenAI or Grok OAuth provider")
	}
	env.Set("CLAUDE_CODE_MODEL_CONTEXT_WINDOWS", encode(string(encode(windows))))
	env.Set("ANTHROPIC_MODEL", encode(value.Main))
	if value.Fable != "" {
		env.Set("ANTHROPIC_DEFAULT_FABLE_MODEL", encode(value.Fable))
	}
	env.Set("ANTHROPIC_DEFAULT_HAIKU_MODEL", encode(value.Haiku))
	env.Set("ANTHROPIC_DEFAULT_SONNET_MODEL", encode(value.Sonnet))
	env.Set("ANTHROPIC_DEFAULT_OPUS_MODEL", encode(value.Opus))
	return env, nil
}
