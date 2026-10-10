package imagegeneration

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

const OpenAIDefaultImageModel = "gpt-image-2"
const GrokDefaultImageModel = "grok-imagine-image-quality"

type Config struct {
	Kind       string `json:"kind"`
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	BaseURL    string `json:"baseUrl,omitempty"`
	APIKey     string `json:"apiKey,omitempty"`
}

func RuntimeConfig(environment map[string]string) *Config {
	trim := func(key string) string { return protocol.TrimJavaScriptString(environment[key]) }
	kind, id, model := trim("CC_HAHA_IMAGE_PROVIDER_KIND"), trim("CC_HAHA_IMAGE_PROVIDER_ID"), trim("CC_HAHA_IMAGE_MODEL")
	if id == "" || model == "" || (kind != "openai_oauth" && kind != "grok_oauth" && kind != "openai_images") {
		return nil
	}
	base, key := trim("CC_HAHA_IMAGE_BASE_URL"), trim("CC_HAHA_IMAGE_API_KEY")
	if kind == "openai_images" && (base == "" || key == "") {
		return nil
	}
	return &Config{Kind: kind, ProviderID: id, Model: model, BaseURL: base, APIKey: key}
}
