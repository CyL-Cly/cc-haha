package server

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
)

type agentMessageSession interface {
	SendAgentMessage(context.Context, string, string) (protocol.JSONObject, error)
}

func (s *Server) sendSubagentMessage(w http.ResponseWriter, r *http.Request, id, ref string, result map[string]any) error {
	data, e := io.ReadAll(r.Body)
	if e != nil || !json.Valid(data) {
		return badRequest("Invalid JSON body")
	}
	if string(data) == "null" {
		return io.ErrUnexpectedEOF
	}
	var body protocol.JSONObject
	_ = body.UnmarshalJSON(data)
	content := trimJS(body.String("content"))
	if content == "" {
		return badRequest("content (string) is required in request body")
	}
	agentID, _ := result["agentId"].(string)
	if agentID == "" {
		return &apiError{409, "CONFLICT", "SubAgent run has no resumable agent id: " + ref}
	}
	live, e := s.live(id)
	if e != nil {
		return e
	}
	control, ok := live.(agentMessageSession)
	if !ok {
		return unsupported("subagent live inbox send_agent_message control")
	}
	response, e := control.SendAgentMessage(r.Context(), agentID, content)
	if e != nil {
		return e
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "ok", Value: json.RawMessage(`true`)})
	for _, field := range response.OwnProperties() {
		_ = out.SetPropertyJSON(field.NameJSON, field.Value)
	}
	writeJSON(w, 200, out)
	return nil
}
