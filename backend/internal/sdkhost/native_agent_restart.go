package sdkhost

import (
	"bytes"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// Only an actual foreground Agent Host can mint its first factory envelope.
// A detached continuation receives immutable bytes and an opaque provenance
// proof, never the foreground Host/Controller/Scope or a rendered API request.
type NativeAgentRestartPrompt struct {
	message            protocol.JSONObject
	agentID, sessionID string
	proof              *nativeAgentRestartProof
}
type nativeAgentRestartProof struct{}

func (h *Host) ExportAgentRestartPrompt(scope *cooperative.Scope) (*NativeAgentRestartPrompt, error) {
	if !scope.BelongsTo(h.options.Owner) || !h.isAgent() || h.options.Print == nil || h.options.Print.AgentID == "" {
		return nil, cooperative.ErrRestricted
	}
	for _, row := range h.serial.History() {
		if row.Type() != "user" {
			continue
		}
		data, e := row.MarshalJSON()
		if e != nil {
			return nil, e
		}
		var original protocol.JSONObject
		if e = original.UnmarshalJSON(data); e != nil {
			return nil, e
		}
		if original.String("uuid") == "" {
			return nil, cap("Agent restart original factory UUID")
		}
		return &NativeAgentRestartPrompt{original.Clone(), h.options.Print.AgentID, h.options.Controller.SessionID(), &nativeAgentRestartProof{}}, nil
	}
	return nil, cap("Agent restart actual foreground prompt")
}
func (h *Host) consumeAgentRestartPrompt(scope *cooperative.Scope, content []byte) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print == nil || h.options.Print.AgentRestartPrompt == nil || h.agentRestartPromptConsumed {
		return nil, nil
	}
	p := h.options.Print.AgentRestartPrompt
	if !scope.BelongsTo(h.options.Owner) || p.proof == nil || !h.isAgent() || h.options.Print.AgentID != p.agentID || h.options.Controller.SessionID() != p.sessionID {
		return nil, cooperative.ErrRestricted
	}
	api, _ := p.message.Object("message")
	if !bytes.Equal(api.Get("content"), content) {
		return nil, cap("Agent restart original prompt identity")
	}
	h.agentRestartPromptConsumed = true
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{p.message.Clone()}}, nil
}
