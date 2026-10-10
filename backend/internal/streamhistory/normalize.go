package streamhistory

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type HistoryMessage struct {
	User      *protocol.JSONObject
	Assistant *Envelope
}

func (message HistoryMessage) MarshalJSON() ([]byte, error) {
	if message.Assistant != nil {
		return message.Assistant.MarshalJSON()
	}
	raw, err := message.User.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
func (message HistoryMessage) MessageJSON() ([]byte, error) {
	if message.Assistant != nil {
		return message.Assistant.MessageJSON()
	}
	return protocol.ParseAndStringifyJavaScriptJSON(message.User.Get("message"))
}

func thinking(block *Block) bool {
	typ := block.Object.String("type")
	return typ == "thinking" || typ == "redacted_thinking"
}
func jsSpace(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == 32 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}

// NormalizeTurn is the observed ordinary-text-user + one provider response
// profile, with tool search and schema corrections absent. It preserves live
// content/usage aliases; it does not replace full normalizeMessagesForAPI.
func NormalizeTurn(user protocol.JSONObject, committed []*Envelope) ([]HistoryMessage, error) {
	api := object(user.Get("message"))
	content := api.Get("content")
	if user.String("type") != "user" || len(content) == 0 || content[0] != '"' || canonical(user.Get("isVirtual")) == "true" {
		return nil, ErrUnsupported
	}
	result := []HistoryMessage{{User: &user}}
	var merged *Envelope
	for _, envelope := range committed {
		if envelope.APIError && envelope.Message.String("model") == messages.SyntheticModel {
			continue
		}
		copy := envelope.clone()
		copy.Content = nil
		for _, block := range envelope.Content {
			if block.Object.String("type") == "tool_use" {
				object := jsonObject(field("type", literal("tool_use")), field("id", block.Object.Get("id")), field("name", block.Object.Get("name")), field("input", block.Object.Get("input")))
				copy.Content = append(copy.Content, &Block{Object: object})
			} else {
				copy.Content = append(copy.Content, block)
			}
		}
		if merged == nil {
			merged = copy
		} else {
			if canonical(merged.Message.Get("id")) != canonical(copy.Message.Get("id")) {
				return nil, ErrUnsupported
			}
			merged.Content = reorder(append(merged.Content, copy.Content...))
		}
	}
	if merged == nil {
		return result, nil
	}
	allThinking := len(merged.Content) > 0
	for _, block := range merged.Content {
		if !thinking(block) {
			allThinking = false
		}
	}
	if allThinking {
		return result, nil
	}
	merged.Content = reorder(merged.Content)
	for len(merged.Content) > 0 && thinking(merged.Content[len(merged.Content)-1]) {
		merged.Content = merged.Content[:len(merged.Content)-1]
	}
	allWhitespace := len(merged.Content) > 0
	for _, block := range merged.Content {
		if block.Object.String("type") != "text" {
			allWhitespace = false
			break
		}
		units, err := protocol.DecodeJavaScriptJSONString(block.Object.Get("text"))
		if err != nil {
			return nil, err
		}
		for _, unit := range units {
			if !jsSpace(unit) {
				allWhitespace = false
			}
		}
	}
	if allWhitespace {
		return result, nil
	}
	return append(result, HistoryMessage{Assistant: merged}), nil
}

// This source reorder retains live Block handles, including replacement of
// their Object fields. JSONObject value copies share initialized object state,
// while this pointer form also preserves the surrounding block identity.
func reorder(content []*Block) []*Block {
	first, last, count := -1, -1, 0
	for index, block := range content {
		if block.Object.String("type") == "tool_use" {
			if first < 0 {
				first = index
			}
			last = index
			count++
		}
	}
	if count < 2 {
		return content
	}
	tools, displaced := []*Block{}, []*Block{}
	for _, block := range content[first : last+1] {
		if block.Object.String("type") == "tool_use" {
			tools = append(tools, block)
		} else {
			displaced = append(displaced, block)
		}
	}
	if len(displaced) == 0 {
		return content
	}
	return append(append(append(append([]*Block{}, content[:first]...), tools...), displaced...), content[last+1:]...)
}
