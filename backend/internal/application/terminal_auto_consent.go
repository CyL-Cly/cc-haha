package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

const nativeAutoDescription = "Auto mode lets Claude handle permission prompts automatically — Claude checks each tool call for risky actions and prompt injection before executing. Actions Claude identifies as safe are executed, while actions Claude identifies as risky are blocked and Claude may try a different approach. Ideal for long-running tasks. Sessions are slightly more expensive. Claude can make mistakes that allow harmful commands to run, it's recommended to only use in isolated environments. Shift+Tab to change mode."

type terminalAutoConsentUI struct {
	request *tui.PermissionRequest
	emit    tui.Emit
	timer   *time.Timer
}

func terminalAutoConsentRequest(pending *clibootstrap.NativeAutoConsent) *tui.PermissionRequest {
	if pending == nil {
		return nil
	}
	decline := "No, go back"
	if pending.Startup {
		decline = "No, exit"
	}
	payload, _ := json.Marshal(map[string]bool{"startup": pending.Startup})
	return &tui.PermissionRequest{ID: pending.ID, Title: "Enable auto mode?", ToolName: "AutoModeConsent", Description: nativeAutoDescription, Payload: payload, Choices: []tui.PermissionChoice{
		{Key: "1", Label: "Yes, and make it my default mode", Decision: tui.PermissionDecision{Allowed: true, Rule: json.RawMessage(`"accept-default"`)}},
		{Key: "2", Label: "Yes, enable auto mode", Decision: tui.PermissionDecision{Allowed: true}},
		{Key: "3", Label: decline, Decision: tui.PermissionDecision{Allowed: false}},
	}}
}

func (s *Session) BindTerminalAutoConsent(ctx context.Context, emit tui.Emit) (*tui.PermissionRequest, func(), error) {
	pending, err := s.process.TerminalAutoConsent(ctx)
	if err != nil {
		return nil, nil, err
	}
	request := terminalAutoConsentRequest(pending)
	s.mu.Lock()
	s.autoConsentUI = &terminalAutoConsentUI{emit: emit, request: request}
	s.mu.Unlock()
	return request, func() {
		s.mu.Lock()
		if s.autoConsentUI != nil {
			if s.autoConsentUI.timer != nil {
				s.autoConsentUI.timer.Stop()
			}
			s.autoConsentUI.emit = nil
		}
		s.mu.Unlock()
	}, nil
}

func (s *Session) scheduleTerminalAutoConsent(ctx context.Context, emit tui.Emit) error {
	pending, err := s.process.TerminalAutoConsent(ctx)
	if err != nil {
		return err
	}
	request := terminalAutoConsentRequest(pending)
	s.mu.Lock()
	if s.autoConsentUI == nil {
		s.autoConsentUI = &terminalAutoConsentUI{}
	}
	ui := s.autoConsentUI
	old := ui.request
	if ui.timer != nil {
		ui.timer.Stop()
		ui.timer = nil
	}
	ui.request = request
	if emit != nil {
		ui.emit = emit
	}
	consumer := ui.emit
	if request != nil {
		ui.timer = time.AfterFunc(400*time.Millisecond, func() {
			s.mu.Lock()
			current := s.autoConsentUI
			if s.closed || current != ui || ui.request != request || ui.emit == nil {
				s.mu.Unlock()
				return
			}
			consumer := ui.emit
			ui.timer = nil
			s.mu.Unlock()
			_ = consumer(tui.Event{Kind: tui.PermissionAsked, Permission: request})
		})
	}
	s.mu.Unlock()
	if old != nil && consumer != nil {
		return consumer(tui.Event{Kind: tui.PermissionClosed, Permission: old, Cancelled: true})
	}
	return nil
}

func (s *Session) respondTerminalAutoConsent(ctx context.Context, id string, decision tui.PermissionDecision) (bool, error) {
	s.mu.Lock()
	ui := s.autoConsentUI
	if ui == nil || ui.request == nil || ui.request.ID != id {
		s.mu.Unlock()
		return false, nil
	}
	request, consumer := ui.request, ui.emit
	s.mu.Unlock()
	var selection string
	_ = json.Unmarshal(decision.Rule, &selection)
	_, exit, err := s.process.ResolveTerminalAutoConsent(ctx, id, decision.Allowed, selection == "accept-default")
	if err != nil {
		return true, err
	}
	s.mu.Lock()
	if s.autoConsentUI == ui && ui.request == request {
		ui.request = nil
		if ui.timer != nil {
			ui.timer.Stop()
			ui.timer = nil
		}
	}
	s.mu.Unlock()
	if consumer != nil {
		err = consumer(tui.Event{Kind: tui.PermissionClosed, Permission: request})
	}
	if err == nil && exit {
		err = tui.ErrStartupConsentDeclined
	}
	return true, err
}
