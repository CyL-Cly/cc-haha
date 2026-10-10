package diagnosticsapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
	"regexp"
	"strings"
	"unicode/utf16"
)

type substitution struct {
	pattern     *regexp2.Regexp
	replacement string
}

func jsRegexp(pattern string) *regexp2.Regexp {
	options := regexp2.ECMAScript
	if strings.HasPrefix(pattern, "(?i)") {
		options |= regexp2.IgnoreCase
		pattern = strings.TrimPrefix(pattern, "(?i)")
	}
	return regexp2.MustCompile(pattern, options)
}

var sanitizers = []substitution{
	{jsRegexp(`(?i)(Bearer\s+)[A-Za-z0-9._~+/-]+`), `${1}[REDACTED]`},
	{jsRegexp(`(?i)([a-z][a-z0-9+.-]*://)([^/?#\s:@]+(?::[^/?#\s@]*)?@)`), `${1}[REDACTED]@`},
	{jsRegexp(`(?i)((?:api[_-]?key|auth[_-]?token|access[_-]?token|refresh[_-]?token|session[_-]?token|token|secret|password)\s*[:=]\s*)[^\s,;"'}]+`), `${1}[REDACTED]`},
	{jsRegexp(`(?i)(ANTHROPIC_(?:API_KEY|AUTH_TOKEN)\s*[:=]\s*)[^\s,;"'}]+`), `${1}[REDACTED]`},
	{jsRegexp(`(?i)([?&](?:api[_-]?key|token|auth|access_token|refresh_token|key)=)[^&\s]+`), `${1}[REDACTED]`},
	{jsRegexp(`\bAKIA[A-Z0-9]{16}\b`), `[REDACTED_AWS_ACCESS_KEY]`},
	{jsRegexp("(?:/Users|/home|/private|/var/folders|/tmp)/[^\\s\"'`]+"), `[REDACTED_PATH]`},
	{jsRegexp("(?i)\\b[A-Z]:\\\\(?:[^\\s\"'`]+\\\\)*[^\\s\"'`]*"), `[REDACTED_PATH]`},
}
var sensitiveKey = regexp.MustCompile(`(?i)(api[_-]?key|auth[_-]?token|access[_-]?token|refresh[_-]?token|session[_-]?token|\btoken\b|secret|password|authorization|cookie|oauth)`)

func (s *Service) SanitizeString(value string, maxLength int) string {
	for _, rule := range sanitizers {
		var err error
		value, err = rule.pattern.Replace(value, rule.replacement, -1, -1)
		if err != nil {
			return "[REDACTED]"
		}
	}
	if s.options.HomeDir != "" {
		value = strings.ReplaceAll(value, s.options.HomeDir, "~")
	}
	units := utf16.Encode([]rune(value))
	if maxLength >= 0 && len(units) > maxLength {
		return string(utf16.Decode(units[:maxLength])) + fmt.Sprintf("...[TRUNCATED %d chars]", len(units)-maxLength)
	}
	return value
}
func (s *Service) sanitizeValue(raw json.RawMessage, depth int) json.RawMessage {
	if depth > 6 {
		return json.RawMessage(`"[TRUNCATED_DEPTH]"`)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '"':
		var text string
		if json.Unmarshal(raw, &text) == nil {
			b, _ := json.Marshal(s.SanitizeString(text, 4096))
			return b
		}
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		if len(values) > 40 {
			values = values[:40]
		}
		for i, v := range values {
			values[i] = s.sanitizeValue(v, depth+1)
		}
		b, _ := json.Marshal(values)
		return b
	case '{':
		var object protocol.JSONObject
		_ = json.Unmarshal(raw, &object)
		result := protocol.NewJSONObject()
		for i, p := range object.OwnProperties() {
			if i >= 80 {
				result.Set("__truncatedKeys", json.RawMessage("true"))
				break
			}
			var name string
			_ = json.Unmarshal(p.NameJSON, &name)
			value := json.RawMessage(`"[REDACTED]"`)
			if !sensitiveKey.MatchString(name) {
				value = s.sanitizeValue(p.Value, depth+1)
			}
			if name == "__proto__" {
				continue
			} // Source assigns into a plain object, invoking its inherited setter.
			_ = result.SetPropertyJSON(p.NameJSON, value)
		}
		b, _ := json.Marshal(result)
		return b
	}
	return raw
}
