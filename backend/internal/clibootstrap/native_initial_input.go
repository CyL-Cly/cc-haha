package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
)

func (m *MainPrint) TakeInitialUserMessage(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		result, err = m.hook.TakeInitialUserMessage(scope)
		return err
	})
	return result, err
}

func (m *MainPrint) nativeSessionStartBase(scope *cooperative.Scope, sid string) (protocol.JSONObject, error) {
	path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if sid != m.Controller.SessionID() {
		path = filepath.Join(filepath.Dir(path), sid+".jsonl")
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "transcript_path", Value: quoteMain(path)}), nil
}

func (m *MainPrint) nativeSessionStartMetadata(scope *cooperative.Scope) (json.RawMessage, json.RawMessage, error) {
	var agentType, model json.RawMessage
	if m.teamWorker != nil {
		agentType = quoteMain(m.teamWorker.preset.AgentType)
	} else if name := m.nativeMainThreadAgentType(); name != "" {
		agentType = quoteMain(name)
	}
	// Source print passes no model; interactive startup supplies the actual
	// resolved initial model. The callback never infers it from a fixed alias.
	if m.options.ForegroundShellProgress && m.Host != nil {
		value, err := m.Host.InferenceModel(scope)
		if err != nil {
			return nil, nil, err
		}
		model = quoteMain(value)
	}
	return agentType, model, nil
}
