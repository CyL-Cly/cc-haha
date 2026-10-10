package sidequestion

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Prepare retains the original parent objects in a separate slice. Only the
// pending-tool placeholder is newly allocated by the same message factory.
func Prepare(history []protocol.JSONObject, factory *messages.Factory) ([]protocol.JSONObject, error) {
	boundary := 0
	for i, record := range history {
		if record.String("type") == "system" && record.String("subtype") == "compact_boundary" {
			boundary = i
		}
	}
	snapshot := append([]protocol.JSONObject{}, history[boundary:]...)
	var last *protocol.JSONObject
	for i := len(snapshot) - 1; i >= 0; i-- {
		if kind := snapshot[i].String("type"); kind == "assistant" || kind == "user" {
			last = &snapshot[i]
			break
		}
	}
	if last != nil && last.String("type") == "assistant" {
		inner := object(last.Get("message"))
		stop := inner.Get("stop_reason")
		if stop == nil || string(stop) == "null" {
			id := string(protocol.StringifyValidatedJavaScriptJSON(inner.Get("id")))
			kept := []protocol.JSONObject{}
			for _, record := range snapshot {
				if record.String("type") != "assistant" || string(protocol.StringifyValidatedJavaScriptJSON(object(record.Get("message")).Get("id"))) != id {
					kept = append(kept, record)
				}
			}
			snapshot = kept
		}
	}
	pending := map[string]json.RawMessage{}
	order := []string{}
	for _, record := range snapshot {
		if kind := record.String("type"); kind != "assistant" && kind != "user" {
			continue
		}
		var content []protocol.JSONObject
		if json.Unmarshal(object(record.Get("message")).Get("content"), &content) != nil {
			continue
		}
		for _, block := range content {
			field := ""
			if block.String("type") == "tool_use" {
				field = "id"
			}
			if block.String("type") == "tool_result" {
				field = "tool_use_id"
			}
			if field == "" {
				continue
			}
			id := block.Get(field)
			key := string(protocol.StringifyValidatedJavaScriptJSON(id))
			if block.String("type") == "tool_use" {
				if _, ok := pending[key]; !ok {
					order = append(order, key)
				}
				pending[key] = id
			} else {
				delete(pending, key)
				kept := order[:0]
				for _, previous := range order {
					if previous != key {
						kept = append(kept, previous)
					}
				}
				order = kept
			}
		}
	}
	if len(pending) > 0 {
		blocks := []protocol.JSONObject{}
		for _, key := range order {
			if id, ok := pending[key]; ok {
				blocks = append(blocks, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "is_error", Value: quote(true)}, protocol.JSONField{Name: "content", Value: quote("This tool is still running in the main conversation. Its result is not available to this side question.")}))
				delete(pending, key)
			}
		}
		snapshot = append(snapshot, messages.OrderedUser(factory.CreateUser(storage.Object{"content": quote(blocks)})))
	}
	return snapshot, nil
}
func object(raw json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(raw)
	return result
}

func PromptMessages(input Input, factory *messages.Factory) ([]protocol.JSONObject, error) {
	var pairs []protocol.JSONObject
	if e := json.Unmarshal(input.History, &pairs); e != nil {
		return nil, e
	}
	if len(pairs) > 20 {
		pairs = pairs[len(pairs)-20:]
	}
	rows := []protocol.JSONObject{}
	for _, pair := range pairs {
		rows = append(rows, messages.OrderedUser(factory.CreateUser(storage.Object{"content": pair.Get("question")})))
		rows = append(rows, factoryAssistant(factory.CreateAssistant(storage.Object{"content": quote([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: pair.Get("response")})})})))
	}
	wrapped, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote(wrappedQuestionPrefix), input.Question}, "")
	if e != nil {
		return nil, e
	}
	rows = append(rows, messages.OrderedUser(factory.CreateUser(storage.Object{"content": wrapped})))
	return rows, nil
}

func factoryAssistant(value storage.Object) protocol.JSONObject {
	data, _ := json.Marshal(value)
	return object(data)
}
