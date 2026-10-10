package server

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// ReadAutoQuestionContext uses the same projected, bounded persisted history as
// the desktop history APIs, without opening an additional model session.
func (s *Server) ReadAutoQuestionContext(ctx context.Context, id, agentID string) (json.RawMessage, error) {
	info, e := s.findSession(ctx, id)
	if e != nil {
		return json.RawMessage(`""`), nil
	}
	messages := []json.RawMessage{}
	if !info.memory {
		limit := float64(60)
		if page, e := s.reader.ReadPage(ctx, info.Path, sessionstore.PageOptions{Limit: &limit}); e == nil {
			messages, _ = projectHistory(page.Entries)
		}
	}
	root := autoquestion.ConversationText(messages, false)
	if agentID == "" {
		return root, nil
	}
	transcript, e := s.readSubagentTranscript(ctx, info, agentID, "")
	if e != nil {
		return json.RawMessage(`""`), nil
	}
	agent := transcript.messages
	if len(agent) > 60 {
		agent = agent[len(agent)-60:]
	}
	return autoquestion.CombineContext(root, autoquestion.ConversationText(agent, true), true), nil
}
