package attachments

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func projectTeammateMailbox(value protocol.JSONObject, factory *messages.Factory) ([]protocol.JSONObject, error) {
	if factory == nil {
		return nil, errors.New("Teammate mailbox requires the owning factory")
	}
	var rows []protocol.JSONObject
	if err := json.Unmarshal(value.Get("messages"), &rows); err != nil || rows == nil {
		return nil, malformed("messages.map is not a function")
	}
	content, err := teamspawn.FormatInteractiveTeammateMessages(rows)
	if err != nil {
		return nil, err
	}
	return []protocol.JSONObject{messages.OrderedUser(factory.CreateUser(storage.Object{"content": content, "isMeta": json.RawMessage("true")}))}, nil
}
