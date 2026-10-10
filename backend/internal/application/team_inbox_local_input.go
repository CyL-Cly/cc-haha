package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"strings"
)

func (s *Session) teammateLocalInput(text string) (bool, error) {
	s.mu.Lock()
	autonomous := strings.HasPrefix(s.turnID, "teammate-inbox-")
	s.mu.Unlock()
	if !autonomous || !strings.Contains(text, "<teammate-message") {
		return false, nil
	}
	rows := []protocol.JSONObject{}
	for _, parsed := range teamspawn.ParseInteractiveTeammateXML(text) {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: parsed.Get("teammateId")}, protocol.JSONField{Name: "text", Value: parsed.Get("content")})
		for _, key := range []string{"color", "summary"} {
			if parsed.Get(key) != nil {
				row.Set(key, parsed.Get(key))
			}
		}
		rows = append(rows, row)
	}
	return true, s.emitEvent(tui.Event{Kind: tui.TeammateMessages, Result: raw(rows)})
}
