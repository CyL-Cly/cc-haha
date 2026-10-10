package imruntime

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// QQDispatchC2C preserves SDK normalization before the adapter's own C2C gate.
func QQDispatchC2C(event string, data protocol.JSONObject) (protocol.JSONObject, bool) {
	if event != "C2C_MESSAGE_CREATE" {
		return protocol.JSONObject{}, false
	}
	author := object(data.Get("author"))
	message := qqObject(qqField("rawEventType", event), qqField("kind", "c2c"), protocol.JSONField{Name: "senderId", Value: author.Get("user_openid")}, protocol.JSONField{Name: "content", Value: data.Get("content")}, protocol.JSONField{Name: "messageId", Value: data.Get("id")}, protocol.JSONField{Name: "timestamp", Value: data.Get("timestamp")}, protocol.JSONField{Name: "attachments", Value: data.Get("attachments")}, protocol.JSONField{Name: "msgType", Value: data.Get("message_type")}, protocol.JSONField{Name: "messageScene", Value: data.Get("message_scene")}, protocol.JSONField{Name: "msgElements", Value: data.Get("msg_elements")}, qqField("raw", data))
	var ext []json.RawMessage
	_ = json.Unmarshal(object(data.Get("message_scene")).Get("ext"), &ext)
	for _, entry := range ext {
		if len(entry) == 0 || entry[0] != '"' {
			continue
		}
		parts := TextJSON(entry).Split('=')
		if len(parts) < 2 {
			continue
		}
		key := parts[0].Trim().String()
		value := JoinText(parts[1:], NewText("=")).Trim()
		if len(value) == 0 {
			continue
		}
		if key == "msg_idx" {
			message.Set("msgIdx", raw(value))
		} else if key == "ref_msg_idx" {
			message.Set("refMsgIdx", raw(value))
		}
	}
	if string(data.Get("message_type")) == "103" {
		for _, el := range collectJSONArray(data.Get("msg_elements")) {
			if truthy(el.Get("msg_idx")) {
				message.Set("refMsgIdx", el.Get("msg_idx"))
				break
			}
		}
	}
	return message, true
}
func ExtractQQPayload(message protocol.JSONObject) (protocol.JSONObject, error) {
	if message.String("kind") != "c2c" || truthy(message.Get("senderIsBot")) {
		return protocol.JSONObject{}, nil
	}
	trim := func(value json.RawMessage) (Text, error) {
		if len(value) == 0 || string(value) == "null" {
			return nil, nil
		}
		if value[0] != '"' {
			return nil, errors.New("QQ payload string field does not support trim")
		}
		return TextJSON(value).Trim(), nil
	}
	user, e := trim(message.Get("senderId"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if len(user) == 0 {
		return protocol.JSONObject{}, nil
	}
	attachments := []protocol.JSONObject{}
	transcripts := []Text{}
	for _, item := range collectJSONArray(message.Get("attachments")) {
		if !truthy(item.Get("url")) {
			continue
		}
		attachments = append(attachments, item)
		text, e := trim(item.Get("asr_refer_text"))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if len(text) > 0 {
			transcripts = append(transcripts, text)
		}
	}
	text, e := trim(message.Get("content"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	texts := []Text{}
	if len(text) > 0 {
		texts = append(texts, text)
	}
	texts = append(texts, transcripts...)
	text = JoinText(texts, NewText("\n")).Trim()
	id, e := trim(message.Get("messageId"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	name, e := trim(message.Get("senderName"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if len(name) == 0 {
		name = NewText("QQ User")
	}
	if len(id) == 0 {
		stamp := message.Get("timestamp")
		if len(stamp) == 0 || string(stamp) == "null" {
			stamp = raw("")
		}
		id = user.Join(NewText(":")).Join(qqString(stamp)).Join(NewText(":")).Join(text.Slice(0, 32))
	}
	remaining := []protocol.JSONObject{}
	for _, item := range attachments {
		if !truthy(item.Get("asr_refer_text")) {
			remaining = append(remaining, item)
		}
	}
	return qqObject(qqField("chatId", user), qqField("userId", user), qqField("displayName", name), qqField("text", text), qqField("dedupKey", id), qqField("attachments", remaining)), nil
}
