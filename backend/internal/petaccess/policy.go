// Package petaccess ports the original companion's HTTP and WebSocket projection.
package petaccess

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const SessionLimit = 9

var statusPath = regexp.MustCompile(`^/api/sessions/([0-9a-zA-Z_-]{1,64})/chat/status$`)
var websocketPath = regexp.MustCompile(`^/ws/([0-9a-zA-Z_-]{1,64})$`)

func allowedAPI(method, path string) bool {
	if (method == "GET" || method == "PUT") && path == "/api/desktop-ui/preferences/pet" {
		return true
	}
	return method == "GET" && (path == "/api/sessions" || statusPath.MatchString(path))
}
func HTTPAllowed(method, path, requestedMethod string) bool {
	if method == "GET" && (path == "/health" || websocketPath.MatchString(path)) {
		return true
	}
	if method == http.MethodOptions {
		return allowedAPI(upperASCII(requestedMethod), path)
	}
	return allowedAPI(method, path)
}
func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
func ScopedSessionID(path string) string {
	for _, p := range []*regexp.Regexp{websocketPath, statusPath} {
		if m := p.FindStringSubmatch(path); m != nil {
			return m[1]
		}
	}
	return ""
}
func jsSpace(u uint16) bool {
	switch u {
	case 0x9, 0xa, 0xb, 0xc, 0xd, 0x20, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return u >= 0x2000 && u <= 0x200a
}
func ClientMessageAllowed(raw json.RawMessage) bool {
	var o protocol.JSONObject
	if o.UnmarshalJSON(raw) != nil {
		return false
	}
	typ := o.String("type")
	keys := []string{"type"}
	switch typ {
	case "user_message":
		units, e := protocol.DecodeJavaScriptJSONString(o.Get("content"))
		if e != nil || len(units) > 32768 {
			return false
		}
		start, end := 0, len(units)
		for start < end && jsSpace(units[start]) {
			start++
		}
		for end > start && jsSpace(units[end-1]) {
			end--
		}
		if start == end || units[start] == '/' {
			return false
		}
		keys = append(keys, "content")
	case "sync_state", "stop_generation", "ping":
	default:
		return false
	}
	if len(o.OwnProperties()) != len(keys) {
		return false
	}
	for _, key := range keys {
		if o.Get(key) == nil {
			return false
		}
	}
	return true
}
func ServerMessage(raw json.RawMessage) json.RawMessage {
	var o protocol.JSONObject
	if o.UnmarshalJSON(raw) != nil {
		return nil
	}
	switch o.String("type") {
	case "connected", "session_state", "pong":
		return raw
	case "permission_requests_snapshot":
		o.Set("toolRequestIds", json.RawMessage(`[]`))
		o.Set("computerUseRequestIds", json.RawMessage(`[]`))
	case "status":
		o = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"status"`)}, protocol.JSONField{Name: "state", Value: o.Get("state")})
	case "message_complete":
		return json.RawMessage(`{"type":"message_complete","usage":{"input_tokens":0,"output_tokens":0}}`)
	case "error":
		out := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"error"`)}, protocol.JSONField{Name: "message", Value: json.RawMessage(`"Pet action failed. Open the session for details."`)})
		for _, name := range []string{"code", "retryable", "businessErrorCode"} {
			if value := o.Get(name); value != nil {
				out.Set(name, value)
			}
		}
		o = out
	default:
		return nil
	}
	b, _ := o.MarshalJSON()
	return b
}
