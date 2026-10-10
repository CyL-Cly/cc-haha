package mcpcli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func serveJSONType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}
func serveTypeIssue(expected string, raw json.RawMessage, path []string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "expected", Value: encode(expected)}, protocol.JSONField{Name: "code", Value: encode("invalid_type")}, protocol.JSONField{Name: "path", Value: encode(path)}, protocol.JSONField{Name: "message", Value: encode(fmt.Sprintf("Invalid input: expected %s, received %s", expected, serveJSONType(raw)))})
}
func serveParamFailure(method string, raw json.RawMessage) *ServeRPCError {
	if method != "initialize" && method != "tools/call" {
		return nil
	}
	issues := []protocol.JSONObject{}
	if serveJSONType(raw) != "object" {
		issues = append(issues, serveTypeIssue("object", raw, []string{"params"}))
	} else {
		params := object(raw)
		if method == "initialize" {
			if serveJSONType(params.Get("protocolVersion")) != "string" {
				issues = append(issues, serveTypeIssue("string", params.Get("protocolVersion"), []string{"params", "protocolVersion"}))
			}
			if serveJSONType(params.Get("capabilities")) != "object" {
				issues = append(issues, serveTypeIssue("object", params.Get("capabilities"), []string{"params", "capabilities"}))
			}
			if serveJSONType(params.Get("clientInfo")) != "object" {
				issues = append(issues, serveTypeIssue("object", params.Get("clientInfo"), []string{"params", "clientInfo"}))
			} else {
				info := object(params.Get("clientInfo"))
				for _, field := range []string{"name", "version"} {
					if serveJSONType(info.Get(field)) != "string" {
						issues = append(issues, serveTypeIssue("string", info.Get(field), []string{"params", "clientInfo", field}))
					}
				}
			}
		} else {
			if serveJSONType(params.Get("name")) != "string" {
				issues = append(issues, serveTypeIssue("string", params.Get("name"), []string{"params", "name"}))
			}
			if args := params.Get("arguments"); args != nil && serveJSONType(args) != "object" {
				issues = append(issues, serveTypeIssue("record", args, []string{"params", "arguments"}))
			}
		}
	}
	if len(issues) == 0 {
		return nil
	}
	var formatted bytes.Buffer
	_ = json.Indent(&formatted, protocol.StringifyValidatedJavaScriptJSON(encode(issues)), "", "  ")
	return &ServeRPCError{Code: -32603, Message: formatted.String()}
}
