package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func questionPermission(t *testing.T, questions string) *State {
	t.Helper()
	s := NewState(nil, 100, 30)
	s.Editor.Set("ask a question")
	s.HandleKey(Key{Name: "enter"}, time.Now())
	s.Apply(Event{TurnID: s.ActiveTurn, Kind: PermissionAsked, Permission: &PermissionRequest{ID: "question-1", ToolName: "AskUserQuestion", Input: json.RawMessage(`{"questions":` + questions + `,"metadata":{"source":"remember","autoAnswered":true}}`)}})
	return s
}

const simpleQuestion = `{"question":"Which?","header":"Choice","options":[{"label":"A","description":"First","preview":"<div>A</div>"},{"label":"B","description":"Second"}]}`

func questionKey(s *State, name, text string) []action {
	return s.HandleKey(Key{Name: name, Text: text}, time.Now())
}
func decisionAnswers(t *testing.T, a []action) protocol.JSONObject {
	t.Helper()
	if len(a) != 1 || a[0].kind != actionPermission || !a[0].decision.Allowed {
		t.Fatalf("missing real answer permission %#v", a)
	}
	in := tuiQuestionObject(a[0].decision.UpdatedInput)
	if tuiQuestionObject(in.Get("metadata")).Get("autoAnswered") != nil {
		t.Fatal("model forged automatic marker")
	}
	return tuiQuestionObject(in.Get("answers"))
}
func TestQuestionSingleSelectPreviewsAndOtherNotes(t *testing.T) {
	s := questionPermission(t, "["+simpleQuestion+"]")
	view := s.View("", false)
	if !strings.Contains(view.Content, "Preview:") || strings.Contains(view.Content, "Allow AskUserQuestion?") {
		t.Fatal("question fell back to generic approval")
	}
	questionKey(s, "y", "")
	if len(s.Permissions) != 1 {
		t.Fatal("generic allow key manufactured an answer")
	}
	a := questionKey(s, "enter", "")
	answers := decisionAnswers(t, a)
	if answers.String("Which?") != "A" {
		t.Fatal("selected answer")
	}
	input := tuiQuestionObject(a[0].decision.UpdatedInput)
	annotations := tuiQuestionObject(input.Get("annotations"))
	annotation := tuiQuestionObject(annotations.Get("Which?"))
	if annotation.String("preview") != "<div>A</div>" {
		t.Fatal("selected preview omitted")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	questionKey(s, "down", "")
	questionKey(s, "down", "")
	s.Paste(" \ufeffChinese 中文\r\nAnswer ")
	a = questionKey(s, "enter", "")
	answers = decisionAnswers(t, a)
	if answers.String("Which?") != " \ufeffChinese 中文\n\nAnswer " {
		t.Fatalf("free answer changed %s", tuiQuestionJSON(answers))
	}
	in := tuiQuestionObject(a[0].decision.UpdatedInput)
	annotation = tuiQuestionObject(tuiQuestionObject(in.Get("annotations")).Get("Which?"))
	if annotation.String("notes") != "Chinese 中文\n\nAnswer" {
		t.Fatal("Source free-text annotation missing")
	}
}
func TestQuestionMultiSelectOrderSummaryAndOther(t *testing.T) {
	s := questionPermission(t, `[{"question":"Features?","header":"Multi","multiSelect":true,"options":[{"label":"A","description":"A"},{"label":"B","description":"B"}]}]`)
	questionKey(s, "down", "")
	questionKey(s, "space", "")
	questionKey(s, "up", "")
	questionKey(s, "space", "")
	questionKey(s, "down", "")
	questionKey(s, "down", "")
	s.Paste("custom answer")
	questionKey(s, "down", "")
	if a := questionKey(s, "enter", ""); len(a) != 0 {
		t.Fatal("multiselect skipped Source Submit tab")
	}
	view := s.View("", false)
	if !strings.Contains(view.Content, "B, A, custom answer") {
		t.Fatal("Source selection order changed", view.Content)
	}
	a := questionKey(s, "enter", "")
	answers := decisionAnswers(t, a)
	if answers.String("Features?") != "B, A, custom answer" {
		t.Fatal("multi answer")
	}
}
func TestQuestionMultipleTabsPartialAnswersCancelAndBinding(t *testing.T) {
	second := strings.ReplaceAll(simpleQuestion, "Which?", "Where?")
	s := questionPermission(t, "["+simpleQuestion+","+second+"]")
	if a := questionKey(s, "enter", ""); len(a) != 0 {
		t.Fatal("multiple questions submitted early")
	}
	questionKey(s, "tab", "")
	a := questionKey(s, "enter", "")
	answers := decisionAnswers(t, a)
	if answers.String("Which?") != "A" || answers.Get("Where?") != nil {
		t.Fatal("partial answers manufactured missing selection")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	a = questionKey(s, "esc", "")
	if len(a) != 1 || a[0].kind != actionCancel || len(s.Permissions) != 0 {
		t.Fatal("Question rejection without feedback must abort like Source PermissionContext")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	action := "select:next"
	s.SetKeybindings(append(s.bindings, Keybinding{Context: "Select", Chord: []Keystroke{{Key: "x"}}, Action: &action}))
	questionKey(s, "x", "")
	a = questionKey(s, "enter", "")
	answers = decisionAnswers(t, a)
	if answers.String("Which?") != "B" {
		t.Fatal("custom select binding ignored")
	}
}
func TestQuestionFeedbackPlanAndClosedRequestReset(t *testing.T) {
	s := questionPermission(t, "["+simpleQuestion+"]")
	questionKey(s, "down", "")
	questionKey(s, "down", "")
	questionKey(s, "down", "")
	a := questionKey(s, "enter", "")
	if len(a) != 1 || a[0].decision.Allowed || a[0].decision.Behavior != "ask" || !strings.Contains(string(a[0].decision.Message), "Start by asking them what they would like to clarify.") {
		t.Fatal("clarify feedback unavailable")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	s.Permissions[0].PermissionMode = "plan"
	for i := 0; i < 4; i++ {
		questionKey(s, "down", "")
	}
	a = questionKey(s, "enter", "")
	if len(a) != 1 || a[0].decision.Allowed || !strings.Contains(string(a[0].decision.Message), "finish the plan") {
		t.Fatal("plan interview completion")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	questionKey(s, "down", "")
	s.Apply(Event{TurnID: s.ActiveTurn, Kind: PermissionClosed, Permission: &PermissionRequest{ID: "question-1"}})
	s.Apply(Event{TurnID: s.ActiveTurn, Kind: PermissionAsked, Permission: &PermissionRequest{ID: "question-1", ToolName: "AskUserQuestion", Input: json.RawMessage(`{"questions":[` + simpleQuestion + `]}`)}})
	a = questionKey(s, "enter", "")
	if decisionAnswers(t, a).String("Which?") != "A" {
		t.Fatal("closed request reused stale selection")
	}
}

func TestQuestionMultiEnterTogglesAndSingleNumericSelect(t *testing.T) {
	s := questionPermission(t, `[{"question":"Features?","header":"Multi","multiSelect":true,"options":[{"label":"A","description":"A"},{"label":"B","description":"B"}]}]`)
	if a := questionKey(s, "enter", ""); len(a) != 0 || s.question.index != 0 || s.question.answers.String("Features?") != "A" {
		t.Fatal("Source multiselect Enter must toggle")
	}
	if a := questionKey(s, "enter", ""); len(a) != 0 || s.question.answers.String("Features?") != "" {
		t.Fatal("Source multiselect repeated Enter must remove")
	}
	s = questionPermission(t, "["+simpleQuestion+"]")
	actions := questionKey(s, "", "２")
	if decisionAnswers(t, actions).String("Which?") != "B" {
		t.Fatal("Source full-width numeric selection")
	}
}
