package desktopoauth

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strings"
)

const OpusModelID = "claude-opus-5-5"
const SonnetModelID = "claude-sonnet-5-5"

func DefaultModel(subscription string) string {
	if subscription == "max" {
		return OpusModelID
	}
	return SonnetModelID
}
func NormalizeExplicitModel(model string) *string {
	trimmed := protocol.TrimJavaScriptString(model)
	if trimmed == "" {
		return nil
	}
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "[1m]") {
		trimmed = trimmed[:len(trimmed)-4]
	} else if strings.HasSuffix(lower, ":1m") {
		trimmed = trimmed[:len(trimmed)-3]
	}
	lower = strings.ToLower(trimmed)
	aliases := map[string]string{"fable": "claude-fable-5", "opus": OpusModelID, "sonnet": SonnetModelID, "haiku": "claude-haiku-4-5", "claude-fable-5": "claude-fable-5", OpusModelID: OpusModelID, SonnetModelID: SonnetModelID, "claude-haiku-4-5": "claude-haiku-4-5"}
	if value, ok := aliases[lower]; ok {
		return &value
	}
	if strings.HasPrefix(lower, "claude-") {
		return &trimmed
	}
	return nil
}
func (s *Service) ResolveRuntimeModel(ctx context.Context, configured json.RawMessage) (*string, error) {
	tokens, err := s.EnsureFreshTokens(ctx)
	if err != nil || tokens == nil {
		return nil, err
	}
	record := tokens.Record()
	if !truthy(record.Get("accessToken")) {
		return nil, nil
	}
	fallback := DefaultModel(record.String("subscriptionType"))
	var model string
	if configured == nil || string(configured) == "null" || json.Unmarshal(configured, &model) != nil {
		return &fallback, nil
	}
	if strings.ToLower(protocol.TrimJavaScriptString(model)) == "opus[1m]" {
		return &fallback, nil
	}
	if explicit := NormalizeExplicitModel(model); explicit != nil {
		return explicit, nil
	}
	return &fallback, nil
}
func numberJSON(n float64) json.RawMessage {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return []byte("null")
	}
	return encode(n)
}
