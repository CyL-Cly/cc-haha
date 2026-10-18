package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeBuddyAttachments(scope *cooperative.Scope, history []queryloop.Message) ([]queryloop.Message, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	global, err := m.Global.Get()
	if err != nil {
		return nil, err
	}
	previous := make([]protocol.JSONObject, 0, len(history))
	for _, message := range history {
		previous = append(previous, message.Record)
	}
	attachment, err := buddy.IntroAttachment(*global, previous)
	if err != nil || attachment == nil {
		return nil, err
	}
	return []queryloop.Message{{Record: m.factory.CreateAttachment(rawMain(*attachment))}}, nil
}
