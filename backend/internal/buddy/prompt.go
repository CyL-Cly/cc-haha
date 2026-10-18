package buddy

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func IntroText(name, species json.RawMessage) json.RawMessage {
	return text(quote("# Companion\n\nA small "), species, quote(" named "), name, quote(" sits beside the user's input box and occasionally comments in a speech bubble. You're not "), name, quote(" — it's a separate watcher.\n\nWhen the user addresses "), name, quote(" directly (by name), its bubble will answer. Your job in that moment is to stay out of the way: respond in ONE line or less, or just answer any part of the message meant for you. Don't explain that you're not "), name, quote(" — they know. Don't narrate what "), name, quote(" might say — the bubble handles that."))
}

func IntroAttachment(global protocol.JSONObject, history []protocol.JSONObject) (*protocol.JSONObject, error) {
	companion, err := Get(global)
	if err != nil || companion == nil || truthy(global.Get("companionMuted")) {
		return nil, err
	}
	name := companion.Get("name")
	for _, message := range history {
		if message.String("type") != "attachment" {
			continue
		}
		attachment, ok := message.Object("attachment")
		if !ok || attachment.String("type") != "companion_intro" {
			continue
		}
		if bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(attachment.Get("name")), protocol.StringifyValidatedJavaScriptJSON(name)) {
			return nil, nil
		}
	}
	value := protocol.NewJSONObject(field("type", "companion_intro"))
	if len(name) > 0 {
		value.Set("name", name)
	}
	value.Set("species", companion.Get("species"))
	return &value, nil
}
