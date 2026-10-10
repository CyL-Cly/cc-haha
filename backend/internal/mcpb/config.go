package mcpb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
)

type ConfigOptions struct {
	Platform, ExtensionPath, PathSeparator string
	SystemDirectories, UserConfig          protocol.JSONObject
	OnArrayWarning                         func(string, json.RawMessage, json.RawMessage)
}

func invalidRequired(value json.RawMessage) bool {
	return value == nil || valueKind(value) == "null" || string(value) == `""`
}
func requiredMissing(manifest, user protocol.JSONObject) bool {
	for _, property := range object(manifest.Get("user_config")).OwnProperties() {
		option := object(property.Value)
		if !truthy(option.Get("required")) {
			continue
		}
		value := rawProperty(user, property.NameJSON)
		if invalidRequired(value) {
			return true
		}
		if valueKind(value) == "array" {
			var items []json.RawMessage
			_ = json.Unmarshal(value, &items)
			if len(items) == 0 {
				return true
			}
			for _, item := range items {
				if invalidRequired(item) {
					return true
				}
			}
		}
	}
	return false
}

func GenerateConfig(manifest protocol.JSONObject, o ConfigOptions) (*protocol.JSONObject, error) {
	server := object(manifest.Get("server"))
	base := object(server.Get("mcp_config"))
	if !base.IsObject() {
		return nil, nil
	}
	result := base.Clone()
	overrides := object(base.Get("platform_overrides"))
	if value := overrides.Get(o.Platform); value != nil {
		platform := object(value)
		for _, key := range []string{"command", "args", "env"} {
			if truthy(platform.Get(key)) {
				result.Set(key, platform.Get(key))
			}
		}
	}
	if requiredMissing(manifest, o.UserConfig) {
		return nil, nil
	}
	separator := o.PathSeparator
	if separator == "" {
		separator = "/"
	}
	variables := protocol.NewJSONObject(protocol.JSONField{Name: "__dirname", Value: quote(o.ExtensionPath)}, protocol.JSONField{Name: "pathSeparator", Value: quote(separator)}, protocol.JSONField{Name: "/", Value: quote(separator)})
	variables.Assign(o.SystemDirectories)
	merged := protocol.NewJSONObject()
	for _, property := range object(manifest.Get("user_config")).OwnProperties() {
		if string(property.NameJSON) == `"__proto__"` {
			continue
		}
		if value := object(property.Value).Get("default"); value != nil {
			_ = merged.SetPropertyJSON(property.NameJSON, value)
		}
	}
	for _, property := range o.UserConfig.OwnProperties() {
		if string(property.NameJSON) != `"__proto__"` {
			_ = merged.SetPropertyJSON(property.NameJSON, property.Value)
		}
	}
	for _, property := range merged.OwnProperties() {
		var value json.RawMessage
		if valueKind(property.Value) == "array" {
			var entries []json.RawMessage
			_ = json.Unmarshal(property.Value, &entries)
			items := []json.RawMessage{}
			for _, entry := range entries {
				converted, err := protocol.JavaScriptStringJSON(entry)
				if err != nil {
					return nil, err
				}
				items = append(items, converted)
			}
			value = encode(items)
		} else {
			converted, err := protocol.JavaScriptStringJSON(property.Value)
			if err != nil {
				return nil, err
			}
			value = converted
		}
		units, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
		key := append(utf16.Encode([]rune("user_config.")), units...)
		_ = variables.SetPropertyJSON(protocol.QuoteJavaScriptUTF16(key), value)
	}
	data, err := replaceVariables(encode(result), variables, o.OnArrayWarning)
	if err != nil {
		return nil, err
	}
	value := object(data)
	return &value, nil
}

func replacementForMatch(replacement []rune, match *regexp2.Match, subject []rune, named bool) []rune {
	result := []rune{}
	for i := 0; i < len(replacement); i++ {
		if replacement[i] != '$' || i+1 >= len(replacement) {
			result = append(result, replacement[i])
			continue
		}
		switch replacement[i+1] {
		case '$':
			result = append(result, '$')
			i++
		case '&':
			result = append(result, match.Runes()...)
			i++
		case '`':
			result = append(result, subject[:match.RuneIndex]...)
			i++
		case '\'':
			result = append(result, subject[match.RuneIndex+match.RuneLength:]...)
			i++
		case '<':
			end := i + 2
			for end < len(replacement) && replacement[end] != '>' {
				end++
			}
			if !named || end == len(replacement) {
				result = append(result, '$')
				continue
			}
			name := string(replacement[i+2 : end])
			if group := match.GroupByName(name); group != nil {
				result = append(result, group.Runes()...)
			}
			i = end
		default:
			number := int(replacement[i+1] - '0')
			if number < 0 || number > 9 {
				result = append(result, '$')
				continue
			}
			take := 1
			if i+2 < len(replacement) && replacement[i+2] >= '0' && replacement[i+2] <= '9' {
				candidate := number*10 + int(replacement[i+2]-'0')
				if candidate > 0 && match.GroupByNumber(candidate) != nil {
					number, take = candidate, 2
				}
			}
			if number == 0 || match.GroupByNumber(number) == nil {
				result = append(result, '$')
				continue
			}
			result = append(result, match.GroupByNumber(number).Runes()...)
			i += take
		}
	}
	return result
}

func regexKey(units []uint16) string {
	var pattern strings.Builder
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xd800 && u <= 0xdfff {
			pattern.WriteString(fmt.Sprintf(`\u%04x`, u))
		} else {
			pattern.WriteRune(rune(u))
		}
	}
	return pattern.String()
}

func replaceVariables(raw json.RawMessage, variables protocol.JSONObject, warn func(string, json.RawMessage, json.RawMessage)) (json.RawMessage, error) {
	switch valueKind(raw) {
	case "string":
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return nil, err
		}
		subject := make([]rune, len(units))
		for i, u := range units {
			subject[i] = rune(u)
		}
		for _, variable := range variables.OwnProperties() {
			key, _ := protocol.DecodeJavaScriptJSONString(variable.NameJSON)
			pattern, err := regexp2.Compile(`\$\{`+regexKey(key)+`\}`, regexp2.ECMAScript)
			if err != nil {
				return nil, err
			}
			match, err := pattern.FindRunesMatch(subject)
			if err != nil {
				return nil, err
			}
			if match == nil {
				continue
			}
			if valueKind(variable.Value) == "array" {
				if warn != nil {
					warn(text(variable.NameJSON), variable.Value, raw)
				}
				continue
			}
			replacementJSON, err := protocol.JavaScriptStringJSON(variable.Value)
			if err != nil {
				return nil, err
			}
			replacementUnits, _ := protocol.DecodeJavaScriptJSONString(replacementJSON)
			replacement := make([]rune, len(replacementUnits))
			for i, u := range replacementUnits {
				replacement[i] = rune(u)
			}
			named := false
			for _, name := range pattern.GetGroupNames() {
				if _, err := strconv.Atoi(name); err != nil {
					named = true
				}
			}
			out := []rune{}
			cursor := 0
			for match != nil {
				out = append(out, subject[cursor:match.RuneIndex]...)
				out = append(out, replacementForMatch(replacement, match, subject, named)...)
				cursor = match.RuneIndex + match.RuneLength
				match, err = pattern.FindNextMatch(match)
				if err != nil {
					return nil, err
				}
			}
			subject = append(out, subject[cursor:]...)
		}
		output := make([]uint16, len(subject))
		for i, r := range subject {
			output[i] = uint16(r)
		}
		return protocol.QuoteJavaScriptUTF16(output), nil
	case "array":
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		result := []json.RawMessage{}
		for _, item := range items {
			if valueKind(item) == "string" {
				units, _ := protocol.DecodeJavaScriptJSONString(item)
				value := string(utf16.Decode(units))
				if strings.HasPrefix(value, "${user_config.") && len(units) > len("${user_config.")+1 && strings.HasSuffix(value, "}") && !strings.Contains(value[:len(value)-1], "}") {
					name := units[2 : len(units)-1]
					replacement := rawProperty(variables, protocol.QuoteJavaScriptUTF16(name))
					if truthy(replacement) {
						if valueKind(replacement) == "array" {
							var values []json.RawMessage
							_ = json.Unmarshal(replacement, &values)
							result = append(result, values...)
						} else {
							result = append(result, replacement)
						}
					} else {
						result = append(result, item)
					}
					continue
				}
			}
			value, err := replaceVariables(item, variables, warn)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return encode(result), nil
	case "object":
		result := protocol.NewJSONObject()
		for _, property := range object(raw).OwnProperties() {
			value, err := replaceVariables(property.Value, variables, warn)
			if err != nil {
				return nil, err
			}
			if string(property.NameJSON) != `"__proto__"` {
				_ = result.SetPropertyJSON(property.NameJSON, value)
			}
		}
		return encode(result), nil
	}
	return raw, nil
}
