package openaiauth

import (
	"bytes"
	"context"
	"math"
	"strconv"
)

type CodexSelection struct {
	HasOpenAIAuth, IsClaudeSubscriber, ForceOpenAICodex, IsOpenAIModel bool
	HasAnthropicAuthToken, HasExplicitAPIKey, HasFallbackAPIKey        bool
}

func ShouldUseCodexTransport(s CodexSelection) bool {
	return s.HasOpenAIAuth && (!s.IsClaudeSubscriber || s.ForceOpenAICodex) && (s.IsOpenAIModel || (!s.HasAnthropicAuthToken && !s.HasExplicitAPIKey && !s.HasFallbackAPIKey))
}

// HasOpenAIAuth is the original shouldUseOpenAICodexAuth's truthiness check,
// including unvalidated legacy secure values. It does not require an access
// token or interpret a model name as proof of an OAuth account.
func HasOpenAIAuth(value *Value) bool {
	if value == nil {
		return false
	}
	raw := bytes.TrimSpace(value.Get("refreshToken"))
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n':
		return false
	case 'f':
		return false
	case 't', '{', '[':
		return true
	case '"':
		return !bytes.Equal(raw, []byte(`""`))
	default:
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number != 0 && !math.IsNaN(number)
	}
}
func (s *Service) HasAuth(ctx context.Context) bool { return HasOpenAIAuth(s.GetCached(ctx)) }
