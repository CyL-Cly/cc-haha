package server

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
)

func (s *Server) captureSlashCommandsSDK(id string, data json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(data) != nil || message.String("type") != "system" || message.String("subtype") != "init" {
		return
	}
	if raw := bytes.TrimSpace(message.Get("slash_commands")); len(raw) == 0 || raw[0] != '[' {
		return
	}
	s.setSessionSlashCommands(id, message.Get("slash_commands"))
}
func (s *Server) captureSlashCommandsNotification(id string, data json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(data) != nil || message.String("type") != "system_notification" || message.String("subtype") != "slash_commands" {
		return
	}
	s.setSessionSlashCommands(id, message.Get("data"))
}
func (s *Server) setSessionSlashCommands(id string, data json.RawMessage) {
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	if current == nil {
		return
	}
	values := NormalizeSessionSlashCommands(data)
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.retired || current.ctx.Err() != nil {
		return
	}
	current.slashCommands = values
}
func (s *Server) resetSlashCommands(id string) {
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	if current == nil {
		return
	}
	current.mu.Lock()
	current.slashCommands = nil
	current.mu.Unlock()
}

// SessionSlashCommands reads the actual SDK init/reload list. Cold, retired or
// evicted runtimes have no preferred CLI metadata. The result is detached.
func (s *Server) SessionSlashCommands(id string) []protocol.JSONObject {
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	if current == nil {
		return []protocol.JSONObject{}
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.retired || current.ctx.Err() != nil || current.closing != nil || (current.live == nil && current.opening == nil) {
		return []protocol.JSONObject{}
	}
	result := make([]protocol.JSONObject, len(current.slashCommands))
	for i, value := range current.slashCommands {
		result[i] = value.Clone()
	}
	return result
}

func (s *Server) sessionSlashCommandsHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	if info.WorkDir == "" {
		return notFound("Session not found: " + info.ID)
	}
	if s.options.SkillSlashCommands == nil {
		return unsupported("session slash commands")
	}
	preferred := s.SessionSlashCommands(info.ID)
	fallback, e := s.options.SkillSlashCommands(request.Context(), info.WorkDir, len(preferred) == 0)
	if e != nil {
		return e
	}
	commands := fallback
	if len(preferred) > 0 {
		commands = slashcommandsapi.Merge(preferred, fallback)
	}
	if commands == nil {
		commands = []slashcommandsapi.Command{}
	}
	writeJSON(writer, http.StatusOK, struct {
		Commands []slashcommandsapi.Command `json:"commands"`
	}{commands})
	return nil
}
