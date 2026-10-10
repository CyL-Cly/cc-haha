package tui

import (
	"strings"
	"testing"
	"time"
)

func TestWorkflowKeywordComposerSourceBindingSuppressesOnlyCurrentPrompt(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	s := NewState(nil, 100, 30)
	s.SetWorkflowKeywordEnabled(true, now)
	s.Editor.Set("ultracode: audit")
	key := Key{Stroke: &Keystroke{Key: "w", Meta: true}}
	if actions := s.HandleKey(key, now); len(actions) != 0 || !s.workflowKeywordSuppressed || !strings.Contains(s.Notification, "ignored for this prompt") {
		t.Fatal(actions, s.Notification, s.workflowKeywordSuppressed)
	}
	if actions := s.HandleKey(key, now); len(actions) != 0 || s.workflowKeywordSuppressed {
		t.Fatal(actions, s.workflowKeywordSuppressed)
	}
	s.HandleKey(key, now)
	actions := s.HandleKey(Key{Name: "enter"}, now)
	if len(actions) != 1 || actions[0].kind != actionSubmit || !actions[0].input.SuppressWorkflowKeyword || actions[0].input.Text != "ultracode: audit" {
		t.Fatal(actions)
	}
	if s.workflowKeywordSuppressed {
		t.Fatal("dismissal survived composer reset")
	}
	s.FinishTurn(actions[0].input.TurnID, nil)
	s.Editor.Set("ultracode: next")
	actions = s.HandleKey(Key{Name: "enter"}, now)
	if len(actions) != 1 || actions[0].input.SuppressWorkflowKeyword {
		t.Fatal("dismissal leaked into fresh prompt", actions)
	}
	s.FinishTurn(actions[0].input.TurnID, nil)
	s.Editor.Set("ultracode: removed")
	s.HandleKey(key, now)
	s.HandleKey(Key{Name: "ctrl+u"}, now)
	if s.workflowKeywordSuppressed {
		t.Fatal("keyword removal kept suppression")
	}
	s.SetWorkflowKeywordEnabled(false, now)
	s.Editor.Set("ultracode: settings off")
	s.HandleKey(key, now)
	if s.workflowKeywordSuppressed {
		t.Fatal("disabled keyword accepted composer opt-out")
	}
	s.SetWorkflowKeywordEnabled(true, now)
	s.Editor.Set("explain `ultracode`")
	s.HandleKey(key, now)
	if s.workflowKeywordSuppressed {
		t.Fatal("literal keyword toggled orchestration")
	}
}
