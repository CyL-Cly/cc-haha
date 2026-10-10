package adapterconfig

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func badPatch(message string) error { return &Error{400, "BAD_REQUEST", message} }
func requiredRecord(raw json.RawMessage, label string) (protocol.JSONObject, error) {
	object := adapterObject(raw)
	if !object.IsObject() {
		return object, badPatch(label + " must be an object")
	}
	return object, nil
}
func patchString(raw json.RawMessage, label string, max int) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, badPatch(label + " must be a string")
	}
	if len(units) > max {
		return nil, badPatch(label + " is too long")
	}
	return raw, nil
}
func trimPatchString(raw json.RawMessage) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func patchStrings(raw json.RawMessage, label string) (json.RawMessage, error) {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) > 1000 {
		return nil, badPatch(label + " must be an array")
	}
	result := []json.RawMessage{}
	for i, value := range values {
		value, err := patchString(value, fmt.Sprintf("%s[%d]", label, i), 1024)
		if err != nil {
			return nil, err
		}
		value = trimPatchString(value)
		if string(value) == `""` {
			return nil, badPatch(fmt.Sprintf("%s[%d] must not be empty", label, i))
		}
		result = append(result, value)
	}
	return adapterJSON(result), nil
}
func patchNumber(raw json.RawMessage) (float64, bool) {
	var n float64
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, false
	}
	return n, true
}
func safePatchInteger(raw json.RawMessage) bool {
	n, ok := patchNumber(raw)
	return ok && math.Trunc(n) == n && math.Abs(n) <= 9007199254740991
}
func patchUsers(raw json.RawMessage, label string, telegram bool) (json.RawMessage, error) {
	if !telegram {
		return patchStrings(raw, label)
	}
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) > 1000 {
		return nil, badPatch(label + " must be an array")
	}
	for i, value := range values {
		n, _ := patchNumber(value)
		if !safePatchInteger(value) || n <= 0 {
			return nil, badPatch(fmt.Sprintf("%s[%d] must be a positive integer", label, i))
		}
	}
	return adapterJSON(values), nil
}
func knownPatchKeys(object protocol.JSONObject, keys []string, label string) error {
	for _, property := range object.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		if !slices.Contains(keys, key) {
			return badPatch("Unknown " + label + " key: " + key)
		}
	}
	return nil
}
func patchPaired(raw json.RawMessage, label string) (json.RawMessage, error) {
	var users []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &users) != nil || len(users) > 1000 {
		return nil, badPatch(label + " must be an array")
	}
	result := []protocol.JSONObject{}
	for i, user := range users {
		prefix := fmt.Sprintf("%s[%d]", label, i)
		source, err := requiredRecord(user, prefix)
		if err != nil {
			return nil, err
		}
		if err = knownPatchKeys(source, []string{"userId", "displayName", "pairedAt"}, prefix); err != nil {
			return nil, err
		}
		id := source.Get("userId")
		units, e := protocol.DecodeJavaScriptJSONString(id)
		if !(e == nil && len(units) > 0 && len(units) <= 1024) && !safePatchInteger(id) {
			return nil, badPatch(prefix + ".userId is invalid")
		}
		name, err := patchString(source.Get("displayName"), prefix+".displayName", 1024)
		if err != nil {
			return nil, err
		}
		n, ok := patchNumber(source.Get("pairedAt"))
		if !ok || n < 0 {
			return nil, badPatch(prefix + ".pairedAt is invalid")
		}
		result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "userId", Value: id}, protocol.JSONField{Name: "displayName", Value: name}, protocol.JSONField{Name: "pairedAt", Value: source.Get("pairedAt")}))
	}
	return adapterJSON(result), nil
}

// ParsePatch mirrors the desktop API schema, including all existing platform
// fields. Runtime support remains independent of configuration persistence.
func ParsePatch(raw json.RawMessage) (protocol.JSONObject, error) {
	body, err := requiredRecord(raw, "request body")
	if err != nil {
		return body, err
	}
	keys := append([]string{"serverUrl", "defaultProjectDir", "allowedProjectRoots"}, Platforms...)
	keys = append(keys, "pairing")
	if err = knownPatchKeys(body, keys, "config"); err != nil {
		return protocol.JSONObject{}, err
	}
	patch := protocol.NewJSONObject()
	for _, entry := range []struct {
		key   string
		limit int
	}{{"serverUrl", 2048}, {"defaultProjectDir", 4096}} {
		if value := body.Get(entry.key); value != nil {
			parsed, e := patchString(value, entry.key, entry.limit)
			if e != nil {
				return patch, e
			}
			patch.Set(entry.key, parsed)
		}
	}
	if value := body.Get("allowedProjectRoots"); value != nil {
		parsed, e := patchStrings(value, "allowedProjectRoots")
		if e != nil {
			return patch, e
		}
		patch.Set("allowedProjectRoots", parsed)
	}
	if raw := body.Get("pairing"); raw != nil {
		source, e := requiredRecord(raw, "pairing")
		if e != nil {
			return patch, e
		}
		if e = knownPatchKeys(source, []string{"code", "expiresAt", "createdAt"}, "pairing"); e != nil {
			return patch, e
		}
		value := source.Get("code")
		if value != nil {
			if string(value) != "null" {
				if _, e = patchString(value, "pairing.code", 64); e != nil {
					return patch, badPatch("pairing.code must be a string or null")
				}
			}
		}
		for _, key := range []string{"expiresAt", "createdAt"} {
			raw := source.Get(key)
			if raw == nil || string(raw) == "null" {
				continue
			}
			n, ok := patchNumber(raw)
			if !ok || n < 0 {
				return patch, badPatch("pairing." + key + " must be a non-negative number or null")
			}
		}
		patch.Set("pairing", adapterJSON(source))
	}
	fields := map[string][]string{"telegram": {"botToken"}, "feishu": {"appId", "appSecret", "encryptKey", "verificationToken", "domain"}, "wechat": {}, "dingtalk": {"clientId", "clientSecret", "endpoint", "permissionCardTemplateId"}, "whatsapp": {}, "wecom": {"botId", "secret"}, "qq": {"appId", "appSecret"}, "slack": {"botToken", "appToken"}}
	for _, platform := range Platforms {
		raw := body.Get(platform)
		if raw == nil {
			continue
		}
		source, e := requiredRecord(raw, platform)
		if e != nil {
			return patch, e
		}
		known := append(append([]string{}, fields[platform]...), "allowedUsers", "pairedUsers", "defaultWorkDir", "allowedProjectRoots")
		if platform == "feishu" {
			known = append(known, "streamingCard")
		}
		if e = knownPatchKeys(source, known, platform); e != nil {
			return patch, e
		}
		out := protocol.NewJSONObject()
		for _, field := range fields[platform] {
			value := source.Get(field)
			if value == nil {
				continue
			}
			if field == "domain" {
				if string(value) != `"feishu"` && string(value) != `"lark"` {
					return patch, badPatch("feishu.domain must be 'feishu' or 'lark'")
				}
				out.Set(field, value)
				continue
			}
			limit := 16384
			if field == "endpoint" {
				limit = 2048
			}
			value, e = patchString(value, platform+"."+field, limit)
			if e != nil {
				return patch, e
			}
			out.Set(field, value)
		}
		if value := source.Get("allowedUsers"); value != nil {
			parsed, e := patchUsers(value, platform+".allowedUsers", platform == "telegram")
			if e != nil {
				return patch, e
			}
			out.Set("allowedUsers", parsed)
		}
		if value := source.Get("pairedUsers"); value != nil {
			parsed, e := patchPaired(value, platform+".pairedUsers")
			if e != nil {
				return patch, e
			}
			out.Set("pairedUsers", parsed)
		}
		if value := source.Get("defaultWorkDir"); value != nil {
			parsed, e := patchString(value, platform+".defaultWorkDir", 4096)
			if e != nil {
				return patch, e
			}
			out.Set("defaultWorkDir", parsed)
		}
		if value := source.Get("allowedProjectRoots"); value != nil {
			parsed, e := patchStrings(value, platform+".allowedProjectRoots")
			if e != nil {
				return patch, e
			}
			out.Set("allowedProjectRoots", parsed)
		}
		if platform == "feishu" && source.Get("streamingCard") != nil {
			value := source.Get("streamingCard")
			if string(value) != "true" && string(value) != "false" {
				return patch, badPatch("feishu.streamingCard must be a boolean")
			}
			out.Set("streamingCard", value)
		}
		patch.Set(platform, adapterJSON(out))
	}
	return patch, nil
}
