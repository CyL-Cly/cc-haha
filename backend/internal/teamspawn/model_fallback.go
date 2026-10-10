package teamspawn

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// HardcodedModelFallback is the actual teammateModel.ts fallback, distinct
// from model aliases or the main request's currently resolved inference model.
func HardcodedModelFallback(env map[string]string) string {
	if mapped := protocol.TrimJavaScriptString(env["ANTHROPIC_DEFAULT_OPUS_MODEL"]); mapped != "" {
		return mapped
	}
	truthy := func(value string) bool {
		switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	if truthy(env["CLAUDE_CODE_USE_BEDROCK"]) {
		return "us.anthropic.claude-opus-4-7-v1"
	}
	for _, name := range []string{"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if truthy(env[name]) {
			return "claude-opus-4-7"
		}
	}
	return "claude-opus-4-8"
}
