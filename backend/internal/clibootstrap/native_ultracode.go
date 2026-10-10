package clibootstrap

import (
	"context"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/ultracode"
)

// Source AppState.ultracode is session-only. Retained history can announce
// transitions, but never restores consent. External transcript filtering
// deliberately removes these attachments in the original persistence layer.
type nativeUltracodeState struct{ active, suppressed bool }

func (m *MainPrint) ultracodeState(scope *cooperative.Scope) (*nativeUltracodeState, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.ultracode == nil {
		m.ultracode = &nativeUltracodeState{active: !m.agentChild && m.options.AgentID == "" && strings.EqualFold(m.options.Arguments.EffortLevel, "ultracode")}
	}
	return m.ultracode, nil
}
func (m *MainPrint) nativeUltracodeActive(scope *cooperative.Scope) (bool, error) {
	s, e := m.ultracodeState(scope)
	if e != nil {
		return false, e
	}
	return s.active, nil
}
func (m *MainPrint) nativeWorkflowKeywordEnabled(scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if !m.options.Interactive || m.workflowDefinitions == nil {
		return false, nil
	}
	enabled, e := m.workflowDefinitions.EnabledOnOwner(scope)
	if e != nil || !enabled {
		return false, e
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return false, e
	}
	return settings == nil || string(settings.Get("workflowKeywordTriggerEnabled")) != "false", nil
}
func (m *MainPrint) NativeWorkflowKeywordEnabled(ctx context.Context) (enabled bool, err error) {
	// Composer/AppState observation is independent of SerialASK input. A pane
	// may already have an inbox continuation suspended on the same owner.
	var receipt *cooperative.Receipt
	receipt, err = m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		value, e := m.nativeWorkflowKeywordEnabled(scope)
		enabled = value
		return e
	})
	if err == nil {
		err = receipt.Wait()
	}
	return
}
func (m *MainPrint) SetNativeWorkflowKeywordSuppression(ctx context.Context, suppressed bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		s, e := m.ultracodeState(scope)
		if e == nil {
			s.suppressed = suppressed
		}
		return e
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}
func (m *MainPrint) nativeWorkflowKeywordAttachment(scope *cooperative.Scope, text string) ([]queryloop.Message, error) {
	s, e := m.ultracodeState(scope)
	if e != nil {
		return nil, e
	}
	enabled, e := m.nativeWorkflowKeywordEnabled(scope)
	if e != nil {
		return nil, e
	}
	if s.suppressed || !enabled || !ultracode.HasKeyword(text) {
		return nil, nil
	}
	if report := m.nativeFileReporter(); report != nil {
		if err := report(scope, analytics.Event{Name: "tengu_workflow_keyword", Metadata: protocol.NewJSONObject()}); err != nil {
			return nil, err
		}
	}
	return []queryloop.Message{{Record: m.factory.CreateAttachment(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("workflow_keyword_request")})))}}, nil
}
func (m *MainPrint) nativeUltracodeAttachments(scope *cooperative.Scope, history []queryloop.Message, agentID string) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if agentID != "" {
		return nil, nil
	}
	active, e := m.nativeUltracodeActive(scope)
	if e != nil {
		return nil, e
	}
	last := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Type() != "attachment" {
			continue
		}
		var a protocol.JSONObject
		_ = a.UnmarshalJSON(history[i].Record.Get("attachment"))
		if a.String("type") == "ultra_effort_enter" || a.String("type") == "ultra_effort_exit" {
			last = a.String("type")
			break
		}
	}
	a := protocol.NewJSONObject()
	if active {
		reminder := "full"
		if last == "ultra_effort_enter" {
			reminder = "short"
		}
		a.Set("type", quoteMain("ultra_effort_enter"))
		a.Set("reminderType", quoteMain(reminder))
	} else if last == "ultra_effort_enter" {
		a.Set("type", quoteMain("ultra_effort_exit"))
	} else {
		return nil, nil
	}
	return []queryloop.Message{{Record: m.factory.CreateAttachment(rawMain(a))}}, nil
}
