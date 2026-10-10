package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var nativeThinkingInteger = regexp.MustCompile(`^[+-]?[0-9]+`)

// Source first derives the default switch, then applies the legacy budget.
// A nonempty invalid environment value suppresses the deprecated CLI flag.
func resolveNativeThinking(settings *protocol.JSONObject, environment map[string]string, legacy ...*float64) requestcontext.Thinking {
	thinking := requestcontext.Thinking{Type: "adaptive"}
	if settings != nil && string(settings.Get("alwaysThinkingEnabled")) == "false" {
		thinking.Type = "disabled"
	}
	var tokens *float64
	original := environment["MAX_THINKING_TOKENS"]
	if original != "" {
		thinking.Type = "disabled"
		value := strings.TrimLeftFunc(original, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
		if prefix := nativeThinkingInteger.FindString(value); prefix != "" {
			v, err := strconv.ParseFloat(prefix, 64)
			if err == nil {
				tokens = &v
			}
		}
	} else if len(legacy) > 0 {
		tokens = legacy[0]
	}
	if tokens != nil {
		if *tokens > 0 {
			thinking = requestcontext.Thinking{Type: "enabled", BudgetTokens: tokens}
		} else if *tokens == 0 {
			thinking = requestcontext.Thinking{Type: "disabled"}
		}
	}
	return thinking
}
