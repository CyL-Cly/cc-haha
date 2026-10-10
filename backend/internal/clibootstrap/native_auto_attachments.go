package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func autoAttachmentCadence(history []queryloop.Message) (turns int, found bool, count int) {
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Type() == "user" && !planRawTruthy(m.Record.Get("isMeta")) && !planToolFeedback(m.Record.Get("message")) {
			turns++
		} else if m.Type() == "attachment" {
			a, _ := m.Record.Object("attachment")
			if a.String("type") == "auto_mode" {
				found = true
				break
			}
			if a.String("type") == "auto_mode_exit" {
				break
			}
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		a, _ := history[i].Record.Object("attachment")
		if a.String("type") == "auto_mode_exit" {
			break
		}
		if a.String("type") == "auto_mode" {
			count++
		}
	}
	return
}
func (m *MainPrint) nativeAutoModeAttachments(scope *cooperative.Scope, history []queryloop.Message, active bool, call *builtinwrite.CallContext) ([]queryloop.Message, error) {
	c, e := m.nativeToolPermissionContext(scope)
	if call != nil && call.PermissionContext != nil {
		c, e = call.PermissionContext(scope)
	}
	if e != nil {
		return nil, e
	}
	if c.String("mode") != "auto" && (c.String("mode") != "plan" || !active) {
		return nil, nil
	}
	turns, found, count := autoAttachmentCadence(history)
	if found && turns < 5 {
		return nil, nil
	}
	kind := "sparse"
	if (count+1)%5 == 1 {
		kind = "full"
	}
	a := protocol.NewJSONObject(fieldMain("type", "auto_mode"), fieldMain("reminderType", kind))
	return []queryloop.Message{{Record: m.factory.CreateAttachment(rawMain(a))}}, nil
}
