package imruntime

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NormalizeWhatsAppContent uses Baileys' five-wrapper limit and priority.
func NormalizeWhatsAppContent(content json.RawMessage) json.RawMessage {
	if !truthy(content) {
		return nil
	}
	for i := 0; i < 5; i++ {
		value := object(content)
		var inner json.RawMessage
		for _, name := range []string{"ephemeralMessage", "viewOnceMessage", "documentWithCaptionMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension", "editedMessage"} {
			if v := value.Get(name); truthy(v) {
				inner = v
				break
			}
		}
		if len(inner) == 0 {
			break
		}
		content = object(inner).Get("message")
	}
	return append(json.RawMessage(nil), content...)
}
func WhatsAppText(content json.RawMessage) json.RawMessage {
	c := object(NormalizeWhatsAppContent(content))
	for _, v := range []json.RawMessage{c.Get("conversation"), object(c.Get("extendedTextMessage")).Get("text"), object(c.Get("imageMessage")).Get("caption"), object(c.Get("videoMessage")).Get("caption"), object(c.Get("documentMessage")).Get("caption")} {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return raw("")
}
func WhatsAppDirectChat(jid Text) bool {
	return strings.HasSuffix(jid.String(), "@s.whatsapp.net") || strings.HasSuffix(jid.String(), "@lid")
}

// WhatsAppInbound describes the original adapter's pre-dedup gate. Empty text
// remains eligible until media collection has run; it is not silently discarded.
func WhatsAppInbound(message protocol.JSONObject) (protocol.JSONObject, error) {
	if !message.IsObject() {
		return protocol.JSONObject{}, errors.New("null is not an object (evaluating 'message.key')")
	}
	key := object(message.Get("key"))
	jid, id := key.Get("remoteJid"), key.Get("id")
	if !truthy(jid) || !truthy(id) || truthy(key.Get("fromMe")) {
		return protocol.JSONObject{}, nil
	}
	if len(jid) == 0 || jid[0] != '"' {
		return protocol.JSONObject{}, errors.New("chatId.endsWith is not a function. (In 'chatId.endsWith(\"@broadcast\")', 'chatId.endsWith' is undefined)")
	}
	chat := TextJSON(jid)
	if !WhatsAppDirectChat(chat) || chat.String() == "status@broadcast" || strings.HasSuffix(chat.String(), "@broadcast") {
		return protocol.JSONObject{}, nil
	}
	idQuoted, e := protocol.JavaScriptStringJSON(id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	text := WhatsAppText(message.Get("message"))
	if len(text) > 0 && text[0] != '"' {
		return protocol.JSONObject{}, errors.New("text.trim is not a function. (In 'text.trim()', 'text.trim' is undefined)")
	}
	name := message.Get("pushName")
	if !truthy(name) {
		name = jid
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "chatId", Value: jid}, protocol.JSONField{Name: "userId", Value: jid}, protocol.JSONField{Name: "displayName", Value: name}, protocol.JSONField{Name: "text", Value: text}, protocol.JSONField{Name: "dedupKey", Value: raw(chat.Join(NewText(":")).Join(TextJSON(idQuoted)))}, protocol.JSONField{Name: "content", Value: NormalizeWhatsAppContent(message.Get("message"))}), nil
}
func FormatWhatsAppOutbound(text Text) Text { return ConvertMarkdownTables(text).Trim() }
func SplitWhatsAppText(text Text, limit int) []Text {
	values := SplitMessage(FormatWhatsAppOutbound(text), limit)
	out := []Text{}
	for _, v := range values {
		if len(v.Trim()) > 0 {
			out = append(out, v)
		}
	}
	return out
}
func WhatsAppThinkingPreview(current, delta Text, limit int) (Text, Text) {
	full := current.Join(delta)
	if limit < 0 {
		limit = 0
	}
	preview := full.Slice(0, min(limit, len(full))).TrimStart()
	text := NewText("思考中...")
	if len(preview) > 0 {
		text = text.Join(NewText("\n")).Join(preview)
	}
	return full, text
}
func WhatsAppReconnectDelay(attempt int) int {
	if attempt >= 5 {
		return 30000
	}
	if attempt < 0 {
		attempt = 0
	}
	return min(30000, 1000<<attempt)
}
func WhatsAppDisconnectStatus(errorValue json.RawMessage) json.RawMessage {
	o := object(errorValue)
	status := object(o.Get("output")).Get("statusCode")
	if len(status) == 0 || string(status) == "null" {
		status = o.Get("statusCode")
	}
	var number float64
	if len(status) == 0 || json.Unmarshal(status, &number) != nil {
		return nil
	}
	return status
}
