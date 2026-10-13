package tui

import (
	"strings"
	"testing"
	"time"
)

func TestDoubleEscapeOpensNativeSelectorAndSummarizeFeedback(t *testing.T) {
	s := NewState(nil, 90, 30)
	now := time.Now()
	if actions := s.HandleKey(Key{Name: "esc"}, now); len(actions) != 0 {
		t.Fatal(actions)
	}
	actions := s.HandleKey(Key{Name: "esc"}, now.Add(10*time.Millisecond))
	if len(actions) != 1 || actions[0].kind != actionMessageSelector {
		t.Fatal("DoubleEsc missing selector action", actions)
	}
	s.OpenMessageSelector(MessageSelectorSnapshot{FileHistoryEnabled: true, Choices: []MessageChoice{{ID: "older", Text: "first authored task"}, {ID: "latest", Text: "latest authored task"}}})
	if s.MessageSelector.selected != 2 {
		t.Fatal("Source current virtual prompt default missing")
	}
	s.HandleKey(Key{Name: "up"}, now)
	s.HandleKey(Key{Name: "enter"}, now)
	if !s.MessageSelector.confirm {
		t.Fatal("selected message didn't open restore options")
	}
	s.HandleKey(Key{Name: "down"}, now)
	s.HandleKey(Key{Name: "enter"}, now)
	if !s.MessageSelector.feedback {
		t.Fatal("summary feedback editor missing")
	}
	s.HandleKey(Key{Text: "preserve Chinese user constraints"}, now)
	actions = s.HandleKey(Key{Name: "enter"}, now)
	if len(actions) != 1 || actions[0].kind != actionMessageSelection || actions[0].messageSelection.ID != "latest" || actions[0].messageSelection.Action != "summarize" || !strings.Contains(actions[0].messageSelection.Feedback, "Chinese") {
		t.Fatal(actions)
	}
	if actions = s.HandleKey(Key{Name: "esc"}, now); len(actions) != 1 || actions[0].kind != actionCancel {
		t.Fatal("summary model cancellation missing", actions)
	}
}
func TestSelectorCurrentCancelCodeChoicesAndDisabledFileHistory(t *testing.T) {
	s := NewState(nil, 60, 20)
	snapshot := MessageSelectorSnapshot{FileHistoryEnabled: true, Choices: []MessageChoice{{ID: "with-code", Text: "task", CanRestoreCode: true}}}
	s.OpenMessageSelector(snapshot)
	s.HandleKey(Key{Name: "enter"}, time.Now())
	if s.MessageSelector != nil {
		t.Fatal("current prompt must close without operation")
	}
	s.OpenMessageSelector(snapshot)
	s.HandleKey(Key{Name: "up"}, time.Now())
	s.HandleKey(Key{Name: "enter"}, time.Now())
	options := s.MessageSelector.options()
	if strings.Join(options, ",") != "both,conversation,code,summarize,nevermind" {
		t.Fatal(options)
	}
	s.HandleKey(Key{Name: "esc"}, time.Now())
	if s.MessageSelector.confirm {
		t.Fatal("escape should return to message list")
	}
	s.HandleKey(Key{Name: "esc"}, time.Now())
	if s.MessageSelector != nil {
		t.Fatal("list escape should close")
	}
	snapshot.FileHistoryEnabled = false
	s.OpenMessageSelector(snapshot)
	s.HandleKey(Key{Name: "up"}, time.Now())
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].messageSelection.Action != "conversation" {
		t.Fatal("disabled checkpoint Source direct restore", actions)
	}
}
