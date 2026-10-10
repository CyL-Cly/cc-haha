package requestcontext

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func IsNativeBedrockEnvironment(name string) bool {
	switch name {
	case "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_BEDROCK_BASE_URL", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_BEARER_TOKEN_BEDROCK", "CLAUDE_CODE_SKIP_BEDROCK_AUTH", "ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION", "DISABLE_INTERLEAVED_THINKING", "CLAUDE_CODE_DISABLE_1M_CONTEXT":
		return true
	}
	return false
}
func nativeBedrockRequestBetas(input Context, extra *protocol.JSONObject, attemptModel, toolSearchBeta string) ([]string, error) {
	if input.Host.NativeBedrockCanonical == nil {
		return nil, unsupported("actual Bedrock canonical model consumer")
	}
	canonical, err := input.Host.NativeBedrockCanonical(input.Options.Model)
	if err != nil {
		return nil, err
	}
	headers, _ := cloudauth.BedrockBetas(input.Options.Model, canonical, input.Host.Env, input.Host.SDKBetas, true)
	if attemptModel == "" {
		attemptModel = input.Options.Model
	}
	canonical, err = input.Host.NativeBedrockCanonical(attemptModel)
	if err != nil {
		return nil, err
	}
	_, body := cloudauth.BedrockBetas(attemptModel, canonical, input.Host.Env, nil, false)
	if toolSearchBeta != "" {
		body = append(body, toolSearchBeta)
	}
	if len(body) > 0 {
		var existing []json.RawMessage
		if json.Unmarshal(extra.Get("anthropic_beta"), &existing) == nil {
			prior := append([]json.RawMessage{}, existing...)
			for _, value := range body {
				found := false
				for _, entry := range prior {
					if stringValue(entry) == value {
						found = true
						break
					}
				}
				if !found {
					existing = append(existing, quoted(value))
				}
			}
			extra.Set("anthropic_beta", rawArray(existing))
		} else {
			raw, _ := json.Marshal(body)
			extra.Set("anthropic_beta", raw)
		}
	}
	return headers, nil
}
