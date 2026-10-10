package imruntime

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Raw objects/arrays are returned unchanged, exactly as helpers.ts does. A
// JSON string is parsed once, and only a non-null object/array survives.
func ParseDingTalkPayload(value json.RawMessage) json.RawMessage {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return nil
	}
	if value[0] == '"' {
		value = []byte(protocol.JavaScriptJSONSourceUTF16(TextJSON(value)))
		value = bytes.TrimSpace(value)
		if !json.Valid(value) {
			return nil
		}
	}
	if len(value) > 0 && (value[0] == '{' || value[0] == '[') {
		return append(json.RawMessage(nil), value...)
	}
	return nil
}
func dingTalkString(value json.RawMessage) Text {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || value[0] != '"' {
		return nil
	}
	return TextJSON(value)
}
func dingTalkClean(value json.RawMessage) Text { return dingTalkString(value).Trim() }
func dingTalkCoerce(value json.RawMessage) Text {
	quoted, e := protocol.JavaScriptStringJSON(value)
	if e != nil {
		return NewText("undefined")
	}
	return TextJSON(quoted)
}
func dingTalkObjectArray(value json.RawMessage) []protocol.JSONObject {
	var values []json.RawMessage
	_ = json.Unmarshal(value, &values)
	result := []protocol.JSONObject{}
	for _, value := range values {
		value = bytes.TrimSpace(value)
		if len(value) > 0 && value[0] == '{' {
			result = append(result, object(value))
		}
	}
	return result
}
func IsDingTalkDirectMessage(value json.RawMessage) bool {
	return object(value).String("conversationType") == "1"
}
func DingTalkSenderID(value json.RawMessage) *Text {
	o := object(value)
	sender := o.Get("senderStaffId")
	if !credentialTruthy(sender) {
		sender = o.Get("senderId")
	}
	if !credentialTruthy(sender) {
		return nil
	}
	text := dingTalkCoerce(sender)
	return &text
}
func DingTalkChatID(value json.RawMessage) *Text {
	if IsDingTalkDirectMessage(value) {
		sender := DingTalkSenderID(value)
		if sender == nil {
			return nil
		}
		id := NewText("dingtalk:dm:").Join(*sender)
		return &id
	}
	conversation := object(value).Get("conversationId")
	if !credentialTruthy(conversation) {
		return nil
	}
	id := NewText("dingtalk:group:").Join(dingTalkCoerce(conversation))
	return &id
}
func ExtractDingTalkText(value json.RawMessage) Text {
	o := object(value)
	for _, pair := range [][2]string{{"text", "content"}, {"markdown", "text"}} {
		v := object(o.Get(pair[0])).Get(pair[1])
		if len(v) > 0 && bytes.TrimSpace(v)[0] == '"' {
			return TextJSON(v).Trim()
		}
	}
	content := object(ParseDingTalkPayload(o.Get("content")))
	if v := content.Get("text"); len(v) > 0 && bytes.TrimSpace(v)[0] == '"' {
		return TextJSON(v).Trim()
	}
	if items := content.Get("richText"); len(items) > 0 && bytes.TrimSpace(items)[0] == '[' {
		parts := []Text{}
		for _, item := range dingTalkObjectArray(items) {
			v := item.Get("text")
			if len(v) > 0 && bytes.TrimSpace(v)[0] == '"' {
				parts = append(parts, TextJSON(v))
			}
		}
		return JoinText(parts, Text{}).Trim()
	}
	return Text{}
}
func ExtractDingTalkAttachments(value json.RawMessage) []protocol.JSONObject {
	o := object(value)
	content := object(ParseDingTalkPayload(o.Get("content")))
	result := []protocol.JSONObject{}
	image := func(v protocol.JSONObject) {
		url, code := dingTalkClean(v.Get("pictureUrl")), dingTalkClean(v.Get("downloadCode"))
		if len(url) == 0 && len(code) == 0 {
			return
		}
		candidate := protocol.NewJSONObject(dingTalkField("kind", "image"))
		if len(url) > 0 {
			candidate.Set("url", raw(url))
		}
		if len(code) > 0 {
			candidate.Set("downloadCode", raw(code))
		}
		result = append(result, candidate)
	}
	switch o.String("msgtype") {
	case "picture":
		image(content)
	case "file":
		code := dingTalkClean(content.Get("downloadCode"))
		name := dingTalkClean(content.Get("fileName"))
		if len(name) == 0 {
			name = NewText("dingtalk-file")
		}
		if len(code) > 0 {
			result = append(result, protocol.NewJSONObject(dingTalkField("kind", "file"), dingTalkField("downloadCode", code), dingTalkField("fileName", name)))
		}
	case "richText":
		items := content.Get("richText")
		if len(items) > 0 && bytes.TrimSpace(items)[0] == '[' {
			for _, item := range dingTalkObjectArray(items) {
				image(item)
			}
		}
	}
	return result
}
func dingTalkField(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func dingTalkWire(value any) []byte { return protocol.StringifyValidatedJavaScriptJSON(raw(value)) }
