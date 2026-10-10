package tui

import (
	"testing"
	"time"
)

func TestIdleHookNotificationDoesNotRequireTurnAcknowledgement(t *testing.T) {
	model := &terminalModel{state: NewState(nil, 90, 24)}
	event := Event{Kind: Notice, Text: "ACTUAL_IDLE_ENV_HOOK_A21", NoticeTimeout: 5 * time.Second}
	model.Update(actionEventMsg{event: event})
	if model.state.Notification != event.Text || model.state.Busy() {
		t.Fatal("idle callback missing", model.state.Notification)
	}
	model.state.Tick(time.Now().Add(6 * time.Second))
	if model.state.Notification != "" {
		t.Fatal("original timeout not applied")
	}
}
