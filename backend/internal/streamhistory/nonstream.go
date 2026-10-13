package streamhistory

import (
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func NormalizeUsage(raw json.RawMessage) json.RawMessage {
	part := object(raw)
	defaults := object(json.RawMessage(emptyUsage))
	result := jsonObject()
	for _, p := range defaults.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		if name == "server_tool_use" || name == "cache_creation" {
			old, incoming := object(p.Value), object(part.Get(name))
			merged := jsonObject()
			for _, nested := range old.OwnProperties() {
				var key string
				_ = json.Unmarshal(nested.NameJSON, &key)
				merged.Set(key, coalesce(incoming.Get(key), nested.Value))
			}
			result.Set(name, compact(merged))
		} else {
			result.Set(name, coalesce(part.Get(name), p.Value))
		}
	}
	return compact(result)
}

func NativeFallbackEnvelope(raw json.RawMessage, requestID *string, uuid func() string, now func() time.Time) (*Envelope, error) {
	var message protocol.JSONObject
	if err := message.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	message.Delete("_request_id")
	message.Set("usage", NormalizeUsage(message.Get("usage")))
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
		return nil, err
	}
	content := make([]*Block, 0, len(blocks))
	for _, block := range blocks {
		content = append(content, &Block{Object: block})
	}
	id := uuid()
	var originalID json.RawMessage
	if requestID != nil {
		originalID = literal(*requestID)
	}
	record := jsonObject(field("message", nil), field("requestId", originalID), field("type", literal("assistant")), field("uuid", literal(id)), field("timestamp", literal(now().UTC().Format("2006-01-02T15:04:05.000Z"))))
	return &Envelope{Record: record, Message: message, Content: content, Usage: &Usage{Raw: message.Get("usage")}, UUID: id}, nil
}
