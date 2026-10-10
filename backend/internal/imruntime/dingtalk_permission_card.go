package imruntime

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const DingTalkPermissionCardRoute = "permission"

func BuildDingTalkPermissionCardParams(tool Text, input json.RawMessage, requestID Text) protocol.JSONObject {
	action := func(allowed bool, always bool) protocol.JSONObject {
		v := protocol.NewJSONObject(dingTalkField("action", "permit"), dingTalkField("requestId", requestID), dingTalkField("allowed", allowed))
		if always {
			v.Set("rule", raw("always"))
		}
		return v
	}
	allow, always, deny := action(true, false), action(true, true), action(false, false)
	jsonText := func(v any) Text { return NewText(string(dingTalkWire(v))) }
	preview := PreviewInput(input, 600)
	return protocol.NewJSONObject(dingTalkField("title", "Claude Code 需要权限确认"), dingTalkField("toolName", tool), dingTalkField("requestId", requestID), dingTalkField("inputPreview", preview), dingTalkField("allowText", "允许一次"), dingTalkField("alwaysText", "永久允许"), dingTalkField("denyText", "拒绝"), dingTalkField("allowValue", jsonText(allow)), dingTalkField("alwaysValue", jsonText(always)), dingTalkField("denyValue", jsonText(deny)), dingTalkField("permissionActions", jsonText([]any{protocol.NewJSONObject(dingTalkField("text", "允许一次"), dingTalkField("value", allow)), protocol.NewJSONObject(dingTalkField("text", "永久允许"), dingTalkField("value", always)), protocol.NewJSONObject(dingTalkField("text", "拒绝"), dingTalkField("value", deny))})), dingTalkField("sys_full_json_obj", jsonText(protocol.NewJSONObject(dingTalkField("order", []string{"title", "toolName", "inputPreview"}), dingTalkField("actions", []string{"allowValue", "alwaysValue", "denyValue"})))), dingTalkField("config", `{"autoLayout":true}`))
}

// JSON has no cycles; traversal retains original Object.values numeric-key
// order and original-first recursion, including parsed nested JSON strings.
func ParseDingTalkPermissionCardAction(value json.RawMessage) *protocol.JSONObject {
	parse := func(v json.RawMessage) json.RawMessage {
		s := dingTalkString(v).Trim()
		if len(s) > 0 && (s[0] == '{' || s[0] == '[') {
			parsed := []byte(protocol.JavaScriptJSONSourceUTF16(s))
			if json.Valid(parsed) {
				return parsed
			}
		}
		return v
	}
	var walk func(json.RawMessage) []json.RawMessage
	walk = func(v json.RawMessage) []json.RawMessage {
		v = parse(v)
		result := []json.RawMessage{v}
		trim := bytes.TrimSpace(v)
		if len(trim) > 0 && trim[0] == '[' {
			var items []json.RawMessage
			_ = json.Unmarshal(trim, &items)
			for _, item := range items {
				result = append(result, walk(item)...)
			}
		} else if len(trim) > 0 && trim[0] == '{' {
			for _, field := range object(trim).OwnProperties() {
				result = append(result, walk(field.Value)...)
			}
		}
		return result
	}
	values := walk(value)
	for _, v := range values {
		if s := dingTalkString(v); s != nil {
			if decision := ParsePermitCallback(s.String()); decision != nil {
				parts := s.Split(':')
				out := protocol.NewJSONObject(dingTalkField("requestId", parts[1]), dingTalkField("allowed", decision.Allowed))
				if decision.Rule != "" {
					out.Set("rule", raw(decision.Rule))
				}
				return &out
			}
			parsed := parse(v)
			if !bytes.Equal(parsed, v) {
				if result := ParseDingTalkPermissionCardAction(parsed); result != nil {
					return result
				}
			}
		}
	}
	read := func(o protocol.JSONObject, names []string) Text {
		for _, name := range names {
			if s := dingTalkClean(o.Get(name)); len(s) > 0 {
				return s
			}
		}
		return nil
	}
	for _, v := range values {
		trim := bytes.TrimSpace(v)
		if len(trim) == 0 || trim[0] != '{' {
			continue
		}
		o := object(v)
		request := read(o, []string{"requestId", "request_id", "permissionRequestId"})
		if len(request) == 0 {
			continue
		}
		action := strings.ToLower(read(o, []string{"action", "actionType", "decision", "value", "actionValue", "command"}).String())
		rule := read(o, []string{"rule"}).String() == "always"
		var allowed *bool
		for _, name := range []string{"allowed", "allow", "approved"} {
			value := o.Get(name)
			if string(value) == "true" || string(value) == "false" {
				b := string(value) == "true"
				allowed = &b
				break
			}
			s := dingTalkString(value)
			if s == nil {
				continue
			}
			switch strings.ToLower(s.String()) {
			case "true", "yes", "allow", "approve", "permit":
				b := true
				allowed = &b
			case "false", "no", "deny", "reject":
				b := false
				allowed = &b
			}
			if allowed != nil {
				break
			}
		}
		if allowed == nil {
			switch action {
			case "allow", "yes", "approve", "approved", "permit":
				b := true
				allowed = &b
			case "always", "allow-always", "approve-always":
				b := true
				allowed = &b
				rule = true
			case "deny", "no", "reject", "rejected":
				b := false
				allowed = &b
				rule = false
			}
		}
		if allowed == nil {
			continue
		}
		result := protocol.NewJSONObject(dingTalkField("requestId", request), dingTalkField("allowed", *allowed))
		if rule {
			result.Set("rule", raw("always"))
		}
		if s := read(o, []string{"outTrackId", "cardInstanceId"}); s != nil {
			result.Set("outTrackId", raw(s))
		}
		if s := read(o, []string{"chatId", "conversationId", "openConversationId"}); s != nil {
			result.Set("chatId", raw(s))
		}
		return &result
	}
	return nil
}
