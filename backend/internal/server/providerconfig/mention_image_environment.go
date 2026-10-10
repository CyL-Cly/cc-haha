package providerconfig

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// MentionImageEnvironment resolves only the original imagegen isEnabled input.
// It is a read-only projection, not an execution environment or an OAuth token
// check. Listing a capability must not authenticate or contact an upstream.
func (s *Service) MentionImageEnvironment(ctx context.Context, id string) map[string]string {
	index, err := s.ReadIndex(ctx)
	if err != nil {
		return map[string]string{}
	}
	if id == "" {
		id = str(index, "activeId")
	}
	if id == "" || id == "claude-official" {
		return map[string]string{}
	}
	if id == "openai-official" {
		return map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": id, "CC_HAHA_IMAGE_MODEL": "gpt-image-2"}
	}
	if id == "grok-official" {
		return map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "grok_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": id, "CC_HAHA_IMAGE_MODEL": "grok-imagine-image-quality"}
	}
	provider, err := s.Provider(ctx, id)
	if err != nil {
		return map[string]string{}
	}
	switch str(provider, "runtimeKind") {
	case "openai_oauth":
		return map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": "openai-official", "CC_HAHA_IMAGE_MODEL": "gpt-image-2"}
	case "grok_oauth":
		return map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "grok_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": "grok-official", "CC_HAHA_IMAGE_MODEL": "grok-imagine-image-quality"}
	}
	return imageGenerationEnvironment(provider)
}

func imageGenerationEnvironment(provider storage.Object) map[string]string {
	image := object(provider["imageGeneration"])
	model := protocol.TrimJavaScriptString(str(image, "model"))
	if model == "" {
		return map[string]string{}
	}
	base, key := protocol.TrimJavaScriptString(str(image, "baseUrl")), protocol.TrimJavaScriptString(str(image, "apiKey"))
	if base == "" {
		base = str(provider, "baseUrl")
	}
	if key == "" {
		key = str(provider, "apiKey")
	}
	return map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_images", "CC_HAHA_IMAGE_PROVIDER_ID": str(provider, "id"), "CC_HAHA_IMAGE_BASE_URL": base, "CC_HAHA_IMAGE_API_KEY": key, "CC_HAHA_IMAGE_MODEL": model}
}
