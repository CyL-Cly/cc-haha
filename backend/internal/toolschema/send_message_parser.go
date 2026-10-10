package toolschema

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parseSendMessageInput(definition Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	base := definition
	copy := *definition.InputSchema
	copy.Fields = definition.InputSchema.Fields[:2]
	base.InputSchema = &copy
	out, e := parseNativeInput(base, in)
	issues := []json.RawMessage{}
	if e != nil {
		if v, ok := e.(*ValidationError); ok {
			_ = json.Unmarshal(v.Issues, &issues)
		} else {
			return protocol.JSONObject{}, e
		}
	}
	message := in.Get("message")
	if _, e := protocol.DecodeJavaScriptJSONString(message); e == nil {
		out.Set("message", message)
	} else {
		value := objectNativeMessage(message)
		typ := value.String("type")
		var branch *Schema
		for _, candidate := range definition.InputSchema.Fields[2].Schema.Members[1].Members {
			if candidate.Fields[0].Schema.Literal != nil && string(candidate.Fields[0].Schema.Literal) == string(quote(typ)) {
				copy := candidate
				branch = &copy
				break
			}
		}
		var nested []json.RawMessage
		if branch != nil {
			parsed, failure := parseNativeInput(Definition{InputSchema: branch}, value)
			if failure == nil {
				out.Set("message", raw(parsed))
			} else if validation, ok := failure.(*ValidationError); ok {
				_ = json.Unmarshal(validation.Issues, &nested)
			} else {
				return protocol.JSONObject{}, failure
			}
		} else {
			nested = []json.RawMessage{raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_union")}, protocol.JSONField{Name: "errors", Value: array([]json.RawMessage{})}, protocol.JSONField{Name: "note", Value: quote("No matching discriminator")}, protocol.JSONField{Name: "discriminator", Value: quote("type")}, protocol.JSONField{Name: "path", Value: array([]string{"type"})}, protocol.JSONField{Name: "message", Value: quote("Invalid input")}))}
			if !value.IsObject() {
				nested = []json.RawMessage{writeInvalidType("object", message, []string{})}
			}
		}
		if len(nested) > 0 {
			issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_union")}, protocol.JSONField{Name: "errors", Value: array([][]json.RawMessage{{writeInvalidType("string", message, []string{})}, nested})}, protocol.JSONField{Name: "path", Value: array([]string{"message"})}, protocol.JSONField{Name: "message", Value: quote("Invalid input")})))
		}
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return out, nil
}
func objectNativeMessage(raw json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(raw)
	return out
}
