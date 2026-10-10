package imruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These fields remain JavaScript UTF-16 strings through extraction and wire
// projection. In particular, a lone surrogate is not an identity replacement.
type WecomInboundPayload struct {
	ChatID   Text `json:"chatId"`
	UserID   Text `json:"userId"`
	Text     Text `json:"text"`
	DedupKey Text `json:"dedupKey"`
}

func wecomCleanText(value json.RawMessage) Text {
	if len(value) == 0 || value[0] != '"' {
		return Text{}
	}
	return TextJSON(value).Trim()
}
func wecomText(body protocol.JSONObject) (Text, error) {
	parts := []Text{}
	for _, name := range []string{"text", "voice"} {
		part := wecomCleanText(object(body.Get(name)).Get("content"))
		if len(part) > 0 {
			parts = append(parts, part)
		}
	}
	items := object(body.Get("mixed")).Get("msg_item")
	if len(items) > 0 && string(items) != "null" {
		var list []json.RawMessage
		if json.Unmarshal(items, &list) != nil {
			return nil, fmt.Errorf("(mixed?.msg_item ?? []).filter is not a function")
		}
		mixed := []Text{}
		for _, rawItem := range list {
			if string(rawItem) == "null" {
				return nil, fmt.Errorf("null is not an object (evaluating 'item.msgtype')")
			}
			item := object(rawItem)
			if item.String("msgtype") == "text" {
				part := wecomCleanText(object(item.Get("text")).Get("content"))
				if len(part) > 0 {
					mixed = append(mixed, part)
				}
			}
		}
		if len(mixed) > 0 {
			parts = append(parts, JoinText(mixed, NewText("\n")))
		}
	}
	return JoinText(parts, NewText("\n")).Trim(), nil
}

func ExtractWecomPayload(body protocol.JSONObject) (*WecomInboundPayload, error) {
	user := wecomCleanText(object(body.Get("from")).Get("userid"))
	if len(user) == 0 || body.String("chattype") == "group" {
		return nil, nil
	}
	text, err := wecomText(body)
	if err != nil {
		return nil, err
	}
	quoted, err := wecomText(object(body.Get("quote")))
	if err != nil {
		return nil, err
	}
	full := text.Clone()
	if len(quoted) > 0 {
		lines := quoted.Split('\n')
		for i := range lines {
			lines[i] = NewText("> ").Join(lines[i])
		}
		full = JoinText(lines, NewText("\n")).Join(NewText("\n\n")).Join(text).Trim()
	}
	key := wecomCleanText(body.Get("msgid"))
	if len(key) == 0 {
		timestamp := body.Get("create_time")
		if len(timestamp) == 0 || string(timestamp) == "null" {
			timestamp = raw("")
		}
		stamp, err := protocol.JavaScriptStringJSON(timestamp)
		if err != nil {
			return nil, err
		}
		key = user.Join(NewText(":")).Join(TextJSON(stamp)).Join(NewText(":")).Join(text.Slice(0, min(32, len(text))))
	}
	return &WecomInboundPayload{user.Clone(), user.Clone(), full, key}, nil
}

func WecomUTF8Length(text Text) int { return len([]byte(text.String())) }

// Source splitMessageByBytes measures TextEncoder bytes but chooses split
// points in UTF-16, preferring paragraph/line/space boundaries in that order.
func SplitWecomMessageByBytes(text Text, maxBytes int) []Text {
	if maxBytes <= 0 || WecomUTF8Length(text) <= maxBytes {
		return []Text{text.Clone()}
	}
	chunks := []Text{}
	remaining := text.Clone()
	for len(remaining) > 0 {
		if WecomUTF8Length(remaining) <= maxBytes {
			chunks = append(chunks, remaining.Clone())
			break
		}
		end := min(len(remaining), maxBytes)
		for end > 0 && WecomUTF8Length(remaining[:end]) > maxBytes {
			end--
		}
		if end <= 0 {
			end = 1
		}
		window := remaining[:end]
		split := window.LastIndex(NewText("\n\n"), len(window))
		if split <= 0 {
			split = window.LastIndex(NewText("\n"), len(window))
		}
		if split <= 0 {
			split = window.LastIndex(NewText(" "), len(window))
		}
		if split <= 0 {
			split = end
		}
		if split < len(remaining) && remaining[split] >= 0xdc00 && remaining[split] <= 0xdfff {
			split--
		}
		if split <= 0 {
			split = end
		}
		chunk := remaining[:split].TrimEnd()
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}
		remaining = remaining[split:].TrimStart()
	}
	return chunks
}

func wecomReqID(frame protocol.JSONObject) Text {
	return TextJSON(object(frame.Get("headers")).Get("req_id"))
}
func wecomKey(text Text) string { return string(protocol.QuoteJavaScriptUTF16(text)) }
func wecomJSONObject(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func wecomField(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}

func wecomStripControl(data []byte) []byte {
	return []byte(strings.Map(func(r rune) rune {
		if r >= 0 && r <= 8 || r >= 11 && r <= 31 {
			return -1
		}
		return r
	}, string(protocol.DecodeJavaScriptUTF8(data))))
}
