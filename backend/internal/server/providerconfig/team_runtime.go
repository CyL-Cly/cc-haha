package providerconfig

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// TeamRuntime resolves the durable human selection locally. It deliberately
// does not use NormalizeRuntimeSelection's deleted-provider fallback: approval
// of a frozen team must fail if its selected provider no longer exists.
func (s *Service) TeamRuntime(ctx context.Context, requested protocol.JSONObject, member string) (protocol.JSONObject, string, error) {
	var provider storage.Object
	var err error
	if requested.String("providerId") != "claude-official" {
		provider, err = s.Provider(ctx, requested.String("providerId"))
		if err != nil {
			return protocol.JSONObject{}, "", err
		}
	}
	model := protocol.TrimJavaScriptString(requested.String("modelId"))
	name := "Claude"
	if provider != nil {
		name = str(provider, "name")
		slot := model
		if slot == "default" {
			slot = "main"
		}
		mapped := str(object(provider["models"]), slot)
		if slices.Contains([]string{"default", "main", "fable", "sonnet", "opus", "haiku"}, model) && mapped == "" {
			return protocol.JSONObject{}, "", fmt.Errorf("Provider has no mapping for %s", model)
		}
		if mapped != "" {
			model = mapped
		}
	} else {
		value := desktopoauth.NormalizeExplicitModel(model)
		model = ""
		if value != nil {
			model = *value
		}
	}
	if model == "" || model == "inherit" || strings.IndexFunc(model, func(r rune) bool { return r <= 0x1f || r == 0xfeff || r != 0x85 && unicode.IsSpace(r) }) >= 0 {
		return protocol.JSONObject{}, "", fmt.Errorf("Choose a concrete model for %s", member)
	}
	if requested.Get("effortLevel") != nil {
		effort := requested.String("effortLevel")
		metadata := provider
		if metadata == nil {
			metadata = storage.Object{"apiFormat": encode("anthropic")}
		} else if string(metadata["apiFormat"]) == "null" || metadata["apiFormat"] == nil {
			metadata = clone(metadata)
			metadata["apiFormat"] = encode("anthropic")
		}
		if !slices.Contains(EffortLevels(), effort) || !slices.Contains(modelReasoning(metadata, model).efforts, effort) {
			return protocol.JSONObject{}, "", fmt.Errorf("Unsupported reasoning effort for %s", member)
		}
	}
	next := requested.Clone()
	next.Set("modelId", encode(model))
	return next, name, nil
}
