package application

import (
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// The SDK wire deliberately omits these attachments. The interactive terminal
// displays the same native query records that Source AttachmentMessage renders.
func (s *Session) nativeQueryYield(scope *cooperative.Scope, yield *queryloop.Yield) error {
	if s.process == nil || !scope.BelongsTo(s.process.Owner) {
		return cooperative.ErrRestricted
	}
	if yield == nil || yield.Message == nil || yield.Message.Type() != "attachment" {
		return nil
	}
	payload := obj(yield.Message.Record.Get("attachment"))
	count := 0
	text := ""
	switch payload.String("type") {
	case "teammate_mailbox":
		if err := s.emitEvent(tui.Event{Kind: tui.TeammateInboxChanged, Result: raw(s.process.Controller.AppState().Inbox)}); err != nil {
			return err
		}
		return s.emitEvent(tui.Event{Kind: tui.TeammateMessages, TeammateOrigin: tui.TeammateMessageAttachment, Result: payload.Get("messages")})
	case "dynamic_skill":
		var names []json.RawMessage
		if err := json.Unmarshal(payload.Get("skillNames"), &names); err != nil {
			return err
		}
		count = len(names)
		text = fmt.Sprintf("Loaded %d %s from %s", count, skillPlural(count), payload.String("displayPath"))
	case "skill_listing":
		if string(payload.Get("isInitial")) == "true" {
			return nil
		}
		if err := json.Unmarshal(payload.Get("skillCount"), &count); err != nil {
			return err
		}
		text = fmt.Sprintf("%d %s available", count, skillPlural(count))
	}
	if text == "" {
		return nil
	}
	return s.emitEvent(tui.Event{Kind: tui.Notice, Text: text})
}
func skillPlural(count int) string {
	if count == 1 {
		return "skill"
	}
	return "skills"
}
