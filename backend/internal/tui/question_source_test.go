package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestQuestionOriginalUISubmitAnnotationsAndFeedback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	type item struct {
		Kind           string              `json:"kind"`
		InputSource    string              `json:"inputSource"`
		QuestionStates protocol.JSONObject `json:"questionStates"`
		Answers        protocol.JSONObject `json:"answers"`
		Plan           bool                `json:"plan,omitempty"`
	}
	question := `{"questions":[` + simpleQuestion + `]}`
	items := []item{
		{Kind: "submit", InputSource: question, QuestionStates: tuiQuestionObject([]byte(`{"Which?":{"selectedValue":"A","textInputValue":""}}`)), Answers: tuiQuestionObject([]byte(`{"Which?":"A"}`))},
		{Kind: "submit", InputSource: question, QuestionStates: tuiQuestionObject([]byte(`{"Which?":{"selectedValue":"A","textInputValue":" \ufeff explain first \u00a0 "}}`)), Answers: tuiQuestionObject([]byte(`{"Which?":"A"}`))},
		{Kind: "submit", InputSource: question, QuestionStates: tuiQuestionObject([]byte(`{"Which?":{"selectedValue":"__other__","textInputValue":" custom answer "}}`)), Answers: tuiQuestionObject([]byte(`{"Which?":" custom answer "}`))},
		{Kind: "clarify", InputSource: question, QuestionStates: protocol.NewJSONObject(), Answers: protocol.NewJSONObject()},
		{Kind: "clarify", InputSource: question, QuestionStates: protocol.NewJSONObject(), Answers: tuiQuestionObject([]byte(`{"Which?":"A"}`))},
		{Kind: "finish", InputSource: question, QuestionStates: protocol.NewJSONObject(), Answers: tuiQuestionObject([]byte(`{"Which?":"A"}`)), Plan: true},
		{Kind: "submit", InputSource: `{"questions":[{"question":"Which?","header":"multi","options":[{"label":"A","description":"a","preview":"<div>A</div>"},{"label":"B","description":"b"}],"multiSelect":true}]}`, QuestionStates: tuiQuestionObject([]byte(`{"Which?":{"selectedValue":["A","B"],"textInputValue":" multi notes "}}`)), Answers: tuiQuestionObject([]byte(`{"Which?":"A, B"}`))},
	}
	actual := []any{}
	for i, item := range items {
		s := NewState(nil, 80, 30)
		q := s.questionFor(PermissionRequest{ID: "source-" + string(rune('a'+i)), ToolName: "AskUserQuestion", Input: []byte(item.InputSource)})
		if q.err != nil {
			t.Fatal(q.err)
		}
		q.answers = item.Answers
		for j, question := range q.questions {
			state, _ := tuiQuestionOwn(item.QuestionStates, question.Get("question"))
			q.editors[j].Set(tuiQuestionObject(state).String("textInputValue"))
		}
		var value any
		switch item.Kind {
		case "submit":
			value = map[string]any{"allowed": true, "updatedInput": json.RawMessage(q.allowInput()), "done": 1}
		case "clarify":
			value = map[string]any{"allowed": false, "message": json.RawMessage(q.feedback(false)), "done": 1}
		case "finish":
			value = map[string]any{"allowed": false, "message": json.RawMessage(q.feedback(true)), "done": 1}
		}
		actual = append(actual, value)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(tuiQuestionJSON(map[string]any{"homeDir": home, "ui": items}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source UI %v %.2400s", err, stderr.String())
	}
	var source struct {
		Outputs      []any
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil {
		t.Fatalf("Source JSON %.2000s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source fixture escaped", source.Capabilities.Blocked)
	}
	var goOutputs any
	_ = json.Unmarshal(tuiQuestionJSON(actual), &goOutputs)
	if !reflect.DeepEqual(goOutputs, source.Outputs) {
		for i, value := range goOutputs.([]any) {
			if !reflect.DeepEqual(value, source.Outputs[i]) {
				t.Errorf("case%d Go=%s Source=%s", i, tuiQuestionJSON(value), tuiQuestionJSON(source.Outputs[i]))
			}
		}
	}
}

func TestQuestionExternalEditorTargetsLiveQuestionOnly(t *testing.T) {
	s := questionPermission(t, "["+simpleQuestion+"]")
	questionKey(s, "down", "")
	questionKey(s, "down", "")
	s.Paste("initial text")
	actions := questionKey(s, "ctrl+g", "")
	if len(actions) != 1 || actions[0].kind != actionEditor || actions[0].permission != "question-1" || actions[0].text != "initial text" || !s.Editing {
		t.Fatal("question editor did not use existing external-editor host", actions)
	}
	s.Editing = false
	s.finishQuestionEditor("different-request", 0, "stale answer", true)
	if s.question.editors[0].Text() != "initial text" {
		t.Fatal("stale editor altered another permission")
	}
	s.finishQuestionEditor("question-1", 0, "updated editor answer", true)
	actions = questionKey(s, "enter", "")
	if decisionAnswers(t, actions).String("Which?") != "updated editor answer" {
		t.Fatal("external answer missing")
	}
}

func TestQuestionFeedbackMatchesActualPermissionContext(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	feedback := []json.RawMessage{tuiQuestionQuote("clarify these questions"), []byte(`"feedback 😀\ud800"`)}
	inputs := []any{}
	type observedDecision struct {
		Behavior string          `json:"behavior"`
		Message  json.RawMessage `json:"message"`
	}
	type observedReject struct {
		Decision observedDecision `json:"decision"`
		Aborted  bool             `json:"aborted"`
	}
	actual := []observedReject{}
	for _, text := range feedback {
		inputs = append(inputs, map[string]any{"feedback": text})
		decision := questionFeedbackDecision(text)
		actual = append(actual, observedReject{Decision: observedDecision{Behavior: decision.Behavior, Message: decision.Message}})
	}
	inputs = append(inputs, map[string]any{})
	state := questionPermission(t, "["+simpleQuestion+"]")
	actions := questionKey(state, "esc", "")
	decision := questionFeedbackDecision(nil)
	actual = append(actual, observedReject{Decision: observedDecision{Behavior: decision.Behavior, Message: decision.Message}, Aborted: len(actions) == 1 && actions[0].kind == actionCancel})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(tuiQuestionJSON(map[string]any{"homeDir": home, "permissionRejects": inputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source PermissionContext %v %.2400s", err, stderr.String())
	}
	var source struct {
		Outputs      json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped or invalid %.2000s", output)
	}
	// Compare with the UTF-16 JSON boundary; ordinary Go string decoding would
	// turn the lone surrogate into U+FFFD and mask a regression.
	want, err := protocol.ParseAndStringifyJavaScriptJSON(source.Outputs)
	got, goErr := protocol.ParseAndStringifyJavaScriptJSON(tuiQuestionJSON(actual))
	if err != nil || goErr != nil || !bytes.Equal(want, got) {
		t.Fatalf("Question PermissionContext differs\nGo %s\nSource %s\n%v %v", got, want, goErr, err)
	}
}
