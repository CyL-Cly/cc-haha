// Package streamhistory ports the default-release, single-response streaming
// envelope boundary in services/api/claude.ts. Transport, retries, tool schema
// correction, attachments, and the query loop remain the caller's responsibility.
package streamhistory

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Block struct{ Object protocol.JSONObject }
type Usage struct{ Raw json.RawMessage }

// Envelope retains the message and usage references that Source yields before
// the response completes. Serialize at the observation/write boundary, rather
// than caching the JSON bytes at content_block_stop.
type Envelope struct {
	Record   protocol.JSONObject
	Message  protocol.JSONObject
	Content  []*Block
	Usage    *Usage
	UUID     string
	APIError bool
}

func literal(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func jsonObject(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, raw json.RawMessage) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw}
}
func compact(object protocol.JSONObject) json.RawMessage { raw, _ := object.MarshalJSON(); return raw }

func (envelope *Envelope) MessageJSON() ([]byte, error) {
	message := envelope.Message.Clone()
	blocks := make([]protocol.JSONObject, 0, len(envelope.Content))
	for _, block := range envelope.Content {
		blocks = append(blocks, block.Object)
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	message.Set("content", raw)
	if envelope.Usage != nil {
		message.Set("usage", envelope.Usage.Raw)
	}
	raw, err = message.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}

func (envelope *Envelope) MarshalJSON() ([]byte, error) {
	raw, err := envelope.MessageJSON()
	if err != nil {
		return nil, err
	}
	copy := envelope.Record.Clone()
	copy.Set("message", raw)
	raw, err = copy.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}

func (envelope *Envelope) clone() *Envelope {
	copy := *envelope
	copy.Record = envelope.Record.Clone()
	copy.Message = envelope.Message.Clone()
	copy.Content = append([]*Block(nil), envelope.Content...)
	return &copy
}

// Timing is already resolved by the upstream request/watchdog clock. The
// frozen parity clock supplies zero; this component does not invent timing or
// use envelope Date allocations as a substitute for Source Date.now spans.
type Timing struct{ TTFTMillis, DecodeMillis int64 }
type Update struct {
	Event    json.RawMessage
	Envelope *Envelope
	Timing   Timing
}
type Emit func(Update) error

func (update Update) EventJSON() ([]byte, error) {
	object := jsonObject(field("type", literal("stream_event")), field("event", update.Event))
	var event protocol.JSONObject
	if err := json.Unmarshal(update.Event, &event); err != nil {
		return nil, err
	}
	if event.String("type") == "message_start" {
		raw, _ := json.Marshal(update.Timing.TTFTMillis)
		object.Set("ttftMs", raw)
	}
	if event.String("type") == "message_stop" {
		raw, _ := json.Marshal(update.Timing.DecodeMillis)
		object.Set("decodeMs", raw)
	}
	raw, err := object.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
