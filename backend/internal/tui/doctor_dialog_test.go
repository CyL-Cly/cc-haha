package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDoctorDialogSourceConfirmationAndFailedReportRemainDismissible(t *testing.T) {
	for _, key := range []string{"enter", "esc"} {
		state := NewState(nil, 80, 24)
		state.Doctor = &DoctorPanel{Text: "Loading diagnostics..."}
		model := &doctorDialogModel{state: state}
		code := tea.KeyEnter
		if key == "esc" {
			code = tea.KeyEscape
		}
		_, command := model.Update(tea.KeyPressMsg{Code: code})
		if !model.dismissed || model.state.Doctor != nil || command == nil {
			t.Fatal("actual Source confirmation did not terminate public Doctor", key)
		}
	}
	state := NewState(nil, 80, 24)
	state.Doctor = &DoctorPanel{Text: "Loading diagnostics..."}
	model := &doctorDialogModel{state: state}
	_, command := model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if command != nil || model.dismissed {
		t.Fatal("unbound key dismisses Doctor")
	}
	model.Update(doctorLoadedMsg{text: "Diagnostics\nactual model/schema report"})
	if !strings.Contains(model.View().Content, "actual model/schema report") {
		t.Fatal("async Doctor report not rendered")
	}
	failure := errors.New("physical diagnostic producer failed")
	model.Update(doctorLoadedMsg{failure: failure})
	if !errors.Is(model.failure, failure) || !strings.Contains(model.View().Content, failure.Error()) {
		t.Fatal("failed report lost its error or became invisible")
	}
	_, command = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil || !model.dismissed || model.state.Doctor != nil {
		t.Fatal("failed asynchronous report cannot be dismissed")
	}
}
