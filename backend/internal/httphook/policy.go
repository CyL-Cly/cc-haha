// Package httphook ports the original HTTP Hook policy and native exchange.
package httphook

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Header struct {
	Name, Value json.RawMessage
}

type Prepared struct {
	URL     json.RawMessage
	Headers []Header
	Blocked json.RawMessage
}

func stringsRaw(value json.RawMessage) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(value, &values); err != nil || values == nil {
		return nil, errors.New("HTTP Hook expected original string array")
	}
	for _, value := range values {
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// ECMAScript's URL-pattern replacement escapes every regexp metacharacter
// except *. Its wildcard has the original non-dotAll newline exclusions.
func URLMatchesPattern(value, pattern json.RawMessage) (bool, error) {
	units, err := protocol.DecodeJavaScriptJSONString(pattern)
	if err != nil {
		return false, err
	}
	text, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return false, err
	}
	encode := func(values []uint16) string {
		var result strings.Builder
		for _, value := range values {
			if value >= 0xd800 && value <= 0xdfff {
				result.WriteRune(rune(value) + 0x10000)
			} else {
				result.WriteRune(rune(value))
			}
		}
		return result.String()
	}
	var expression strings.Builder
	expression.WriteString("^(?:")
	for _, value := range units {
		if value == '*' {
			expression.WriteString("[^\\r\\n\\x{2028}\\x{2029}]*")
		} else {
			expression.WriteString(regexp.QuoteMeta(encode([]uint16{value})))
		}
	}
	expression.WriteString(")$")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return false, err
	}
	return compiled.MatchString(encode(text)), nil
}

func rawEqual(a, b json.RawMessage) bool {
	x, e := protocol.DecodeJavaScriptJSONString(a)
	if e != nil {
		return false
	}
	y, e := protocol.DecodeJavaScriptJSONString(b)
	if e != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func SanitizeHeaderValue(value json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return nil, err
	}
	clean := make([]uint16, 0, len(units))
	for _, unit := range units {
		if unit != 0 && unit != '\r' && unit != '\n' {
			clean = append(clean, unit)
		}
	}
	return protocol.QuoteJavaScriptUTF16(clean), nil
}

func variableStart(value uint16) bool { return value == '_' || value >= 'A' && value <= 'Z' }
func variablePart(value uint16) bool  { return variableStart(value) || value >= '0' && value <= '9' }

func InterpolateEnvVars(value json.RawMessage, allowed []json.RawMessage, environment map[string]string) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return nil, err
	}
	out := make([]uint16, 0, len(units))
	for i := 0; i < len(units); {
		if units[i] != '$' {
			out = append(out, units[i])
			i++
			continue
		}
		start := i + 1
		braced := start < len(units) && units[start] == '{'
		if braced {
			start++
		}
		if start >= len(units) || !variableStart(units[start]) {
			out = append(out, units[i])
			i++
			continue
		}
		end := start + 1
		for end < len(units) && variablePart(units[end]) {
			end++
		}
		if braced && (end == len(units) || units[end] != '}') {
			out = append(out, units[i])
			i++
			continue
		}
		name := protocol.QuoteJavaScriptUTF16(units[start:end])
		permitted := false
		for _, value := range allowed {
			if rawEqual(name, value) {
				permitted = true
				break
			}
		}
		if permitted {
			key := string(runesASCII(units[start:end]))
			value := environment[key]
			raw, _ := json.Marshal(value)
			replacement, err := protocol.DecodeJavaScriptJSONString(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, replacement...)
		}
		i = end
		if braced {
			i++
		}
	}
	return SanitizeHeaderValue(protocol.QuoteJavaScriptUTF16(out))
}
func runesASCII(values []uint16) []byte {
	result := make([]byte, len(values))
	for i, v := range values {
		result[i] = byte(v)
	}
	return result
}

func Prepare(hook, settings protocol.JSONObject, environment map[string]string) (Prepared, error) {
	prepared := Prepared{URL: hook.Get("url"), Headers: []Header{{Name: json.RawMessage(`"Content-Type"`), Value: json.RawMessage(`"application/json"`)}}}
	if _, err := protocol.DecodeJavaScriptJSONString(prepared.URL); err != nil {
		return prepared, err
	}
	if allowed := settings.Get("allowedHttpHookUrls"); allowed != nil {
		patterns, err := stringsRaw(allowed)
		if err != nil {
			return prepared, err
		}
		matched := false
		for _, pattern := range patterns {
			ok, err := URLMatchesPattern(prepared.URL, pattern)
			if err != nil {
				return prepared, err
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			prepared.Blocked, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{json.RawMessage(`"HTTP hook blocked: "`), prepared.URL, json.RawMessage(`" does not match any pattern in allowedHttpHookUrls"`)}, "")
			return prepared, nil
		}
	}
	if raw := hook.Get("headers"); raw != nil && string(raw) != "null" {
		var headers protocol.JSONObject
		if err := headers.UnmarshalJSON(raw); err != nil || !headers.IsObject() {
			return prepared, errors.New("HTTP Hook expected original header object")
		}
		allowed := []json.RawMessage{}
		if raw := hook.Get("allowedEnvVars"); raw != nil && string(raw) != "null" {
			var err error
			allowed, err = stringsRaw(raw)
			if err != nil {
				return prepared, err
			}
		}
		if raw := settings.Get("httpHookAllowedEnvVars"); raw != nil {
			policy, err := stringsRaw(raw)
			if err != nil {
				return prepared, err
			}
			effective := []json.RawMessage{}
			for _, value := range allowed {
				for _, entry := range policy {
					if rawEqual(value, entry) {
						effective = append(effective, value)
						break
					}
				}
			}
			allowed = effective
		}
		for _, field := range headers.OwnProperties() {
			value, err := InterpolateEnvVars(field.Value, allowed, environment)
			if err != nil {
				return prepared, err
			}
			replaced := false
			for i, old := range prepared.Headers {
				if rawEqual(old.Name, field.NameJSON) {
					prepared.Headers[i].Value = value
					replaced = true
					break
				}
			}
			if !replaced {
				prepared.Headers = append(prepared.Headers, Header{Name: field.NameJSON, Value: value})
			}
		}
	}
	return prepared, nil
}
