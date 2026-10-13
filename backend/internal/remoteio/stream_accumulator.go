package remoteio

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type streamAccumulator struct {
	byMessage map[string]map[int][]uint16
	byScope   map[string]string
}

func newStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{byMessage: map[string]map[int][]uint16{}, byScope: map[string]string{}}
}
func scopeKey(message protocol.JSONObject) string {
	session, _ := protocol.DecodeJavaScriptJSONString(message.Get("session_id"))
	parent, _ := protocol.DecodeJavaScriptJSONString(message.Get("parent_tool_use_id"))
	units := append(append(append([]uint16{}, session...), ':'), parent...)
	return string(protocol.QuoteJavaScriptUTF16(units))
}
func streamMessageID(value json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	if len(units) == 0 {
		return ""
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}
func (a *streamAccumulator) clear(message protocol.JSONObject) {
	messageID := streamMessageID(object(message.Get("message")).Get("id"))
	delete(a.byMessage, messageID)
	scope := scopeKey(message)
	if a.byScope[scope] == messageID {
		delete(a.byScope, scope)
	}
}
func (a *streamAccumulator) accumulate(buffer []protocol.JSONObject) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	touched := map[string]int{}
	for _, message := range buffer {
		event := object(message.Get("event"))
		scope := scopeKey(message)
		switch event.String("type") {
		case "message_start":
			id := streamMessageID(object(event.Get("message")).Get("id"))
			if previous := a.byScope[scope]; previous != "" {
				delete(a.byMessage, previous)
			}
			a.byScope[scope] = id
			a.byMessage[id] = map[int][]uint16{}
			out = append(out, message)
		case "content_block_delta":
			delta := object(event.Get("delta"))
			var blocks map[int][]uint16
			if id := a.byScope[scope]; id != "" {
				blocks = a.byMessage[id]
			}
			if delta.String("type") != "text_delta" || blocks == nil {
				out = append(out, message)
				continue
			}
			var index int
			_ = json.Unmarshal(event.Get("index"), &index)
			units, _ := protocol.DecodeJavaScriptJSONString(delta.Get("text"))
			blocks[index] = append(blocks[index], units...)
			key := a.byScope[scope] + ":" + strconv.Itoa(index)
			if previous, ok := touched[key]; ok {
				snapshot := out[previous]
				updated := object(snapshot.Get("event"))
				text := object(updated.Get("delta"))
				text.Set("text", protocol.QuoteJavaScriptUTF16(blocks[index]))
				updated.Set("delta", raw(text))
				snapshot.Set("event", raw(updated))
				out[previous] = snapshot
				continue
			}
			snapshot := protocol.NewJSONObject()
			for _, key := range []string{"type", "uuid", "session_id", "parent_tool_use_id"} {
				snapshot.Set(key, message.Get(key))
			}
			text := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text_delta")}, protocol.JSONField{Name: "text", Value: protocol.QuoteJavaScriptUTF16(blocks[index])})
			updated := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("content_block_delta")}, protocol.JSONField{Name: "index", Value: event.Get("index")}, protocol.JSONField{Name: "delta", Value: raw(text)})
			snapshot.Set("event", raw(updated))
			touched[key] = len(out)
			out = append(out, snapshot)
		default:
			out = append(out, message)
		}
	}
	return out
}
