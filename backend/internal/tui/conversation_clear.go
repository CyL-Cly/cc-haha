package tui

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// A clear result resets the visible conversation, not the independent input
// draft, pending queue, readline history, or background task projections.
func (s *State) applyConversationCleared(raw json.RawMessage) {
	value := presentationObject(raw)
	var history []protocol.JSONObject
	_ = json.Unmarshal(value.Get("history"), &history)
	s.SetInitialHistory(history, true)
	s.SessionName, s.AgentColor = "", ""
	s.LocalPanel = nil
	s.Scroll = 0
}
