package preflightcore

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const NativeDirectRelayProfile = "native-direct-relay-signature-model-v1"

// This profile owns direct Anthropic-compatible third-party alias defaults.
// Cloud providers, OAuth catalog and modelOverrides remain separate consumers.
func nativeSignatureModel(raw json.RawMessage, env map[string]string) (string, error) {
	if !isString(raw) {
		return model(raw)
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	space := func(u uint16) bool {
		return u == '\ufeff' || strings.ContainsRune(" \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", rune(u))
	}
	trim := func(input []uint16) []uint16 {
		for len(input) > 0 && space(input[0]) {
			input = input[1:]
		}
		for len(input) > 0 && space(input[len(input)-1]) {
			input = input[:len(input)-1]
		}
		return input
	}
	units = trim(units)
	lower := func(input []uint16) ([]uint16, error) {
		out := append([]uint16{}, input...)
		for i, u := range out {
			if u >= 'A' && u <= 'Z' {
				out[i] = u + ('a' - 'A')
			} else if u > 127 && (unicode.IsUpper(rune(u)) || unicode.IsTitle(rune(u))) {
				return nil, capability("native non-ASCII model case conversion")
			}
		}
		return out, nil
	}
	normalized, err := lower(units)
	if err != nil {
		return "", err
	}
	tagged := false
	if !nativeEnvTruthy(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
		for i := 0; i+4 <= len(normalized); i++ {
			if string(protocol.QuoteJavaScriptUTF16(normalized[i:i+4])) == `"[1m]"` {
				tagged = true
				break
			}
		}
		if len(normalized) >= 3 && string(protocol.QuoteJavaScriptUTF16(normalized[len(normalized)-3:])) == `":1m"` {
			tagged = true
		}
	}
	aliasUnits := normalized
	if tagged && len(aliasUnits) >= 4 && string(protocol.QuoteJavaScriptUTF16(aliasUnits[len(aliasUnits)-4:])) == `"[1m]"` {
		aliasUnits = trim(aliasUnits[:len(aliasUnits)-4])
	}
	alias := string(protocol.QuoteJavaScriptUTF16(aliasUnits))
	defaultModel := func(tier, fallback string) string {
		if value := env["ANTHROPIC_DEFAULT_"+tier+"_MODEL"]; value != "" {
			return value
		}
		return fallback
	}
	opus := defaultModel("OPUS", "claude-opus-4-7")
	resolved := ""
	switch alias {
	case `"opusplan"`, `"sonnet"`:
		resolved = defaultModel("SONNET", "claude-sonnet-4-5-20250929")
	case `"haiku"`:
		resolved = defaultModel("HAIKU", "claude-haiku-4-5-20251001")
	case `"opus"`:
		resolved = opus
	case `"fable"`, `"best"`:
		resolved = defaultModel("FABLE", opus)
	}
	if resolved != "" {
		encoded, _ := json.Marshal(resolved)
		units, _ = protocol.DecodeJavaScriptJSONString(encoded)
	}
	// normalizeModelStringForAPI strips both markers anywhere. Remaining :1m
	// stays significant because Source removes only the square-bracket form.
	output := []uint16{}
	for i := 0; i < len(units); i++ {
		if i+4 <= len(units) && units[i] == '[' && (units[i+1] == '1' || units[i+1] == '2') && (units[i+2] == 'm' || units[i+2] == 'M') && units[i+3] == ']' {
			i += 3
			continue
		}
		output = append(output, units[i])
	}
	output, err = lower(trim(output))
	return string(protocol.QuoteJavaScriptUTF16(output)), err
}

func nativeEnvTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
