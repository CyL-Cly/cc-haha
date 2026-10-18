package tui

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The in-session REPL resume callback replaces displayed messages and clears
// its input. It keeps readline history, queued inputs, tasks and permissions.
func (s *State) applyTerminalConversationResumed(data json.RawMessage) {
	value := presentationObject(data)
	var records []protocol.JSONObject
	_ = json.Unmarshal(value.Get("history"), &records)
	s.SetInitialHistory(records, true)
	s.Editor.Reset()
	s.LocalPanel = nil
	s.Scroll = 0
	s.SessionName, s.AgentColor = "", ""
}
