package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeSelectedHookModel(scope *cooperative.Scope, hook protocol.JSONObject) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if value := hook.Get("model"); value != nil && string(value) != "null" {
		return hook.String("model"), nil
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return "", err
	}
	environment := map[string]string{}
	snapshot := m.environment.Snapshot()
	for _, field := range snapshot.OwnProperties() {
		var key, value string
		_ = json.Unmarshal(field.NameJSON, &key)
		_ = json.Unmarshal(field.Value, &value)
		environment[key] = value
	}
	selector, err := modelselection.NewParsing(modelselection.Options{Environment: environment, Settings: settings.Clone()})
	if err != nil {
		return "", err
	}
	return selector.SmallFastModel(), nil
}

func (m *MainPrint) nativeAgentHookTranscript(scope *cooperative.Scope, call queryhooks.Context) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
	if err != nil {
		return "", err
	}
	if call.AgentID == "" || call.AgentID == m.options.AgentID {
		return path, nil
	}
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		path, err = nativeAgentOwnerCall(m, scope, d.preset.parent, func(current *cooperative.Scope) (string, error) {
			return d.preset.parent.Controller.Writer().ComputedTranscriptPath(current)
		})
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-"+call.AgentID+".jsonl"), nil
}

func (m *MainPrint) nativeAgentHookBindings() *queryhooks.AgentBindings {
	if m.options.Native == nil || !m.options.Native.NativeProcess {
		return nil
	}
	return &queryhooks.AgentBindings{Owner: m.Owner, Transcript: m.nativeAgentHookTranscript, Model: m.nativeSelectedHookModel, Query: func(ctx context.Context, scope *cooperative.Scope, input queryhooks.AgentModelInput, receive func(queryloop.Message) (bool, error)) error {
		if m.Host == nil {
			return cooperative.ErrRestricted
		}
		return m.Host.QueryAgentHook(ctx, scope, input, receive)
	}, Clear: func(scope *cooperative.Scope, id string) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		m.hook.ClearNativeAgentHooks(id)
		return nil
	}}
}
