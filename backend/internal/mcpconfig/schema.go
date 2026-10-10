// Package mcpconfig implements the original MCP configuration producer.
package mcpconfig

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	return o
}
func encode(v any) json.RawMessage   { raw, _ := json.Marshal(v); return raw }
func quote(v string) json.RawMessage { return encode(v) }
func isString(raw json.RawMessage) bool {
	_, err := protocol.DecodeJavaScriptJSONString(raw)
	return err == nil
}
func truthy(raw json.RawMessage) bool {
	if raw == nil {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}
func list(raw json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	_ = json.Unmarshal(raw, &out)
	return out
}
func name(p protocol.JSONProperty) string {
	var value string
	_ = json.Unmarshal(p.NameJSON, &value)
	return value
}

// ValidateServer returns relative issue paths from the original union schema.
// It strips unknown object fields and retains original declaration ordering.
func ValidateServer(raw json.RawMessage) (protocol.JSONObject, []string) {
	o := object(raw)
	if !o.IsObject() {
		return protocol.JSONObject{}, []string{""}
	}
	kind := o.String("type")
	if v := o.Get("type"); v != nil && (!isString(v) || kind == "") {
		return protocol.JSONObject{}, []string{""}
	}
	result := protocol.NewJSONObject()
	if v := o.Get("type"); v != nil {
		result.Set("type", v)
	}
	aborted := false
	issues := []string{}
	stringField := func(k string, required bool) {
		v := o.Get(k)
		if v == nil && !required {
			return
		}
		if !isString(v) {
			aborted = true
			return
		}
		result.Set(k, v)
	}
	stringArray := func(k string, defaultEmpty bool) {
		v := o.Get(k)
		if v == nil {
			if defaultEmpty {
				result.Set(k, []byte("[]"))
			}
			return
		}
		if !strings.HasPrefix(string(v), "[") {
			aborted = true
			return
		}
		for _, item := range list(v) {
			if !isString(item) {
				aborted = true
				return
			}
		}
		result.Set(k, v)
	}
	stringMap := func(k string) {
		v := o.Get(k)
		if v == nil {
			return
		}
		source := object(v)
		if !source.IsObject() {
			aborted = true
			return
		}
		out := protocol.NewJSONObject()
		for _, p := range source.OwnProperties() {
			if !isString(p.Value) {
				aborted = true
				return
			}
			if name(p) != "__proto__" {
				_ = out.SetPropertyJSON(p.NameJSON, p.Value)
			}
		}
		result.Set(k, encode(out))
	}
	booleanField := func(k string) {
		v := o.Get(k)
		if v == nil {
			return
		}
		if string(v) != "true" && string(v) != "false" {
			aborted = true
			return
		}
		result.Set(k, v)
	}
	switch kind {
	case "", "stdio":
		stringField("command", true)
		if isString(o.Get("command")) && o.String("command") == "" {
			issues = append(issues, "command")
		}
		stringArray("args", true)
		stringMap("env")
	case "sse", "http", "ws":
		stringField("url", true)
		stringMap("headers")
		stringField("headersHelper", false)
		if kind != "ws" && o.Get("oauth") != nil {
			config := object(o.Get("oauth"))
			if !config.IsObject() {
				aborted = true
				break
			}
			parsed := protocol.NewJSONObject()
			for _, k := range []string{"clientId", "callbackPort", "authServerMetadataUrl", "xaa"} {
				v := config.Get(k)
				if v == nil {
					continue
				}
				switch k {
				case "clientId":
					if !isString(v) {
						aborted = true
					}
				case "callbackPort":
					var n float64
					if json.Unmarshal(v, &n) != nil || math.IsInf(n, 0) || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 {
						aborted = true
					} else if n <= 0 {
						issues = append(issues, "oauth.callbackPort")
					}
				case "authServerMetadataUrl":
					if !isString(v) {
						aborted = true
					} else {
						url := config.String(k)
						if _, err := weburl.Parse(url); err != nil {
							issues = append(issues, "oauth.authServerMetadataUrl")
						}
						if !strings.HasPrefix(url, "https://") {
							issues = append(issues, "oauth.authServerMetadataUrl")
						}
					}
				case "xaa":
					if string(v) != "true" && string(v) != "false" {
						aborted = true
					}
				}
				parsed.Set(k, v)
			}
			result.Set("oauth", encode(parsed))
		}
	case "sse-ide", "ws-ide":
		stringField("url", true)
		stringField("ideName", true)
		if kind == "ws-ide" {
			stringField("authToken", false)
		}
		booleanField("ideRunningInWindows")
	case "sdk":
		stringField("name", true)
	case "claudeai-proxy":
		stringField("url", true)
		stringField("id", true)
	default:
		aborted = true
	}
	if aborted {
		return protocol.JSONObject{}, []string{""}
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, issues
	}
	return result, nil
}
