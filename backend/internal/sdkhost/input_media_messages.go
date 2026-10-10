package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (h *Host) appendInputMediaMetadata(scope *cooperative.Scope, user protocol.JSONObject, texts []json.RawMessage) ([]protocol.JSONObject, error) {
	values := []protocol.JSONObject{user}
	text, present, err := originalInputAttachmentText(&user)
	if err != nil {
		return nil, err
	}
	var attached []queryloop.Message
	if present {
		if h.options.Print.InputAttachments != nil {
			attached, err = h.options.Print.InputAttachments(h.inputModelContext, scope, text, h.serial.History())
		} else if h.options.Print.PlanAttachments != nil {
			attached, err = h.options.Print.PlanAttachments(scope, h.serial.History())
		}
	}
	if err != nil {
		return nil, err
	}
	for _, message := range attached {
		if message.Type() != "attachment" {
			return nil, cap("input media original initial attachment")
		}
		values = append(values, message.Record)
	}
	blocks := make([]protocol.JSONObject, len(texts))
	for i, text := range texts {
		blocks[i] = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: text})
	}
	return append(values, messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": raw(blocks), "isMeta": raw(true)}))), nil
}
