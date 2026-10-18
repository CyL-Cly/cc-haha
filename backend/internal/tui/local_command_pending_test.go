package tui

import (
	"errors"
	"testing"
	"time"
)

func TestTerminalLocalCommandAdmissionBeforeFirstUIAndCompletionRetiresOnlyOwnedToken(t *testing.T) {
	s := NewState([]Completion{{Name: "resume", Kind: "local-jsx"}, {Name: "btw", Kind: "local-jsx"}}, 100, 35)
	s.Editor.Set("/resume actual-uuid")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].localCommandToken == 0 || !s.Busy() {
		t.Fatal("Actual command reservation missing before first UI", actions)
	}
	token := actions[0].localCommandToken
	s.Editor.Set("NEXT_HUMAN_BEFORE_ACK")
	if a := s.HandleKey(Key{Name: "enter"}, time.Now()); len(a) != 0 || len(s.Queue) != 1 || s.ActiveTurn != "" {
		t.Fatal("Next human escaped asynchronous command admission", a, s.Queue, s.ActiveTurn)
	}
	if a := s.finishTerminalLocalCommand(token+1, nil); len(a) != 0 || !s.Busy() {
		t.Fatal("Unowned callback released admission")
	}
	s.Apply(Event{Kind: ConversationResumed, Result: []byte(`{"history":[],"sessionId":"new-sid"}`)})
	if a := s.nextTerminalQueuedInput(); len(a) != 0 {
		t.Fatal("Resume projection released queued human before actual onDone", a)
	}
	a := s.finishTerminalLocalCommand(token, nil)
	if len(a) != 1 || a[0].kind != actionSubmit || a[0].input.Text != "NEXT_HUMAN_BEFORE_ACK" || len(s.Queue) != 0 {
		t.Fatal("Actual onDone did not release original queued human", a, s.Queue)
	}
	if a = s.finishTerminalLocalCommand(token, nil); len(a) != 0 {
		t.Fatal("Duplicate completion released additional work")
	}
}
func TestTerminalLocalCommandMountedPanelAndImmediateBusyDoNotReleaseOrCancelParent(t *testing.T) {
	s := NewState([]Completion{{Name: "mobile", Kind: "local-jsx"}, {Name: "btw", Kind: "local-jsx"}}, 100, 35)
	token := s.beginTerminalLocalCommand("command:panel:mobile\x00")
	s.Queue = []Input{{Text: "AFTER_MODAL", TurnID: "queued-human"}}
	s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &LocalPanel{ID: "owned", Kind: "mobile"}})
	if a := s.finishTerminalLocalCommand(token, nil); len(a) != 0 || s.Busy() {
		t.Fatal("Mounted panel admission receipt did not preserve panel gate", a)
	}
	s.Apply(Event{Kind: LocalPanelClosed})
	if a := s.nextTerminalQueuedInput(); len(a) != 1 || a[0].input.Text != "AFTER_MODAL" {
		t.Fatal("Actual unmount did not wake retained queue", a)
	}
	if token = s.beginTerminalLocalCommand("command:panel:btw\x00question"); token != 0 {
		t.Fatal("Immediate command overwrote running parent reservation", token)
	}
	s.ActiveTurn = ""
	s.Queue = nil
	token = s.beginTerminalLocalCommand("command:panel:resume\x00bad")
	if a := s.finishTerminalLocalCommand(token, errors.New("actual command error")); len(a) != 0 || s.Busy() || s.Status != "actual command error" {
		t.Fatal("Failed command left admission blocked", a, s.Status)
	}
}
