package application

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// Ink consumes processUserInput's messages even when shouldQuery is false.
// The SDK protocol deliberately emits no synthetic assistant for that branch.
// Deliver the same actual visible users through the internal terminal seam.
func (s *Session) localInputMessages(records []protocol.JSONObject) error {
	texts := []string{}
	processed := []tui.LocalProcessedMessage{}
	hasWarning := false
	for _, record := range records {
		if record.String("type") == "system" && record.String("subtype") == "local_command" {
			processed = append(processed, tui.LocalProcessedMessage{Kind: "system", Text: record.String("content"), Level: record.String("level")})
			hasWarning = true
			continue
		}
		if messages.IsNativeUserPromptBlockedRecord(record) {
			processed = append(processed, tui.LocalProcessedMessage{Kind: "system", Text: record.String("content"), Level: record.String("level")})
			hasWarning = true
			continue
		}
		if record.String("type") != "user" || string(record.Get("isMeta")) == "true" {
			continue
		}
		message := obj(record.Get("message"))
		var text string
		if json.Unmarshal(message.Get("content"), &text) == nil && text != "" {
			if handled, err := s.teammateLocalInput(text); handled || err != nil {
				if err != nil {
					return err
				}
				continue
			}
			texts = append(texts, text)
			processed = append(processed, tui.LocalProcessedMessage{Kind: "user", Text: text})
		}
	}
	if hasWarning {
		return s.emitEvent(tui.Event{Kind: tui.LocalProcessedMessages, LocalMessages: processed})
	}
	if len(texts) == 0 {
		return nil
	}
	return s.emitEvent(tui.Event{Kind: tui.LocalUserMessages, UserMessages: texts})
}
