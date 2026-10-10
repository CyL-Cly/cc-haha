package mcpruntime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ResultCapabilityError struct{ Boundary string }

func (e *ResultCapabilityError) Error() string {
	return "native MCP result capability is not migrated: " + e.Boundary
}
func resultRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func resultObject(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
func textBlock(text json.RawMessage) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: resultRaw("text")}, protocol.JSONField{Name: "text", Value: text})
}
func appendText(parts ...json.RawMessage) json.RawMessage {
	units := []uint16{}
	for _, part := range parts {
		next, _ := protocol.DecodeJavaScriptJSONString(part)
		units = append(units, next...)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func jsonEqual(a, b json.RawMessage) bool {
	a, b = bytesTrim(a), bytesTrim(b)
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	if a[0] == '{' && b[0] == '{' {
		x, y := resultObject(a).OwnProperties(), resultObject(b).OwnProperties()
		if len(x) != len(y) {
			return false
		}
		for _, p := range x {
			found := false
			for _, q := range y {
				if string(p.NameJSON) == string(q.NameJSON) {
					if !jsonEqual(p.Value, q.Value) {
						return false
					}
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if a[0] == '[' && b[0] == '[' {
		var x, y []json.RawMessage
		_ = json.Unmarshal(a, &x)
		_ = json.Unmarshal(b, &y)
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	if a[0] == '"' && b[0] == '"' {
		return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
	}
	if (a[0] == '-' || a[0] >= '0' && a[0] <= '9') && (b[0] == '-' || b[0] >= '0' && b[0] <= '9') {
		n, _ := strconv.ParseFloat(string(a), 64)
		m, _ := strconv.ParseFloat(string(b), 64)
		return n == m
	}
	return string(a) == string(b)
}
func bytesTrim(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.Trim(string(raw), " \r\t\n"))
}
func resultWhitespace(u uint16) bool {
	return u >= 9 && u <= 13 || u == 0x20 || u == 0xa0 || u == 0x1680 || u >= 0x2000 && u <= 0x200a || u == 0x2028 || u == 0x2029 || u == 0x202f || u == 0x205f || u == 0x3000 || u == 0xfeff
}

// TransformResult retains toolResult precedence, the original text/resource
// projection and semantic structured-content deduplication. Media production
// is an explicit boundary until the native resizing/persistence producer is
// bound; unsupported media cannot silently disappear from the model request.
func TransformResult(result protocol.JSONObject, tool, server string) (json.RawMessage, error) {
	if value := result.Get("toolResult"); value != nil {
		return protocol.JavaScriptStringJSON(value)
	}
	if raw := result.Get("content"); raw != nil && strings.HasPrefix(string(raw), "[") {
		var source []protocol.JSONObject
		if err := json.Unmarshal(raw, &source); err != nil {
			return nil, err
		}
		content := []protocol.JSONObject{}
		for _, block := range source {
			switch block.String("type") {
			case "text":
				content = append(content, textBlock(block.Get("text")))
			case "resource_link":
				text := appendText(resultRaw("[Resource link: "), block.Get("name"), resultRaw("] "), block.Get("uri"))
				if value := block.Get("description"); value != nil && string(value) != `""` {
					text = appendText(text, resultRaw(" ("), value, resultRaw(")"))
				}
				content = append(content, textBlock(text))
			case "resource":
				resource := resultObject(block.Get("resource"))
				if value := resource.Get("text"); value != nil {
					content = append(content, textBlock(appendText(resultRaw("[Resource from "+server+" at "), resource.Get("uri"), resultRaw("] "), value)))
				} else if resource.Get("blob") != nil {
					return nil, &ResultCapabilityError{"resource blob persistence/resizing"}
				}
			case "audio", "image":
				return nil, &ResultCapabilityError{block.String("type") + " persistence/resizing"}
			}
		}
		if structured := result.Get("structuredContent"); structured != nil {
			serialized := protocol.StringifyValidatedJavaScriptJSON(structured)
			present := false
			for _, block := range content {
				units, _ := protocol.DecodeJavaScriptJSONString(block.Get("text"))
				for len(units) > 0 && resultWhitespace(units[0]) {
					units = units[1:]
				}
				for len(units) > 0 && resultWhitespace(units[len(units)-1]) {
					units = units[:len(units)-1]
				}
				trimmed := protocol.JavaScriptJSONSourceUTF16(units)
				if trimmed == string(serialized) {
					present = true
					break
				}
				if json.Valid([]byte(trimmed)) {
					if jsonEqual(json.RawMessage(trimmed), structured) {
						present = true
						break
					}
				}
			}
			if !present {
				content = append(content, textBlock(resultRaw("Structured content:\n"+string(serialized))))
			}
		}
		return resultRaw(content), nil
	}
	if value := result.Get("structuredContent"); value != nil {
		return resultRaw(string(protocol.StringifyValidatedJavaScriptJSON(value))), nil
	}
	return nil, fmt.Errorf("MCP server \"%s\" tool \"%s\": unexpected response format", server, tool)
}
