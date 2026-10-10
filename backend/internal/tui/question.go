package tui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type questionState struct {
	id        string
	input     protocol.JSONObject
	questions []protocol.JSONObject
	answers   protocol.JSONObject
	index     int
	focus     []int
	selected  [][]int
	editors   []Editor
	notes     []bool
	err       error
}

func tuiQuestionObject(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(value, &out)
	return out
}
func tuiQuestionJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func tuiQuestionQuote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func tuiQuestionEquals(a, b json.RawMessage) bool {
	x, e := protocol.DecodeJavaScriptJSONString(a)
	y, f := protocol.DecodeJavaScriptJSONString(b)
	return e == nil && f == nil && reflect.DeepEqual(x, y)
}
func tuiQuestionOwn(value protocol.JSONObject, key json.RawMessage) (json.RawMessage, bool) {
	for _, property := range value.OwnProperties() {
		if tuiQuestionEquals(property.NameJSON, key) {
			return property.Value, true
		}
	}
	return nil, false
}
func (s *State) questionFor(request PermissionRequest) *questionState {
	if s.question != nil && s.question.id == request.ID {
		return s.question
	}
	definition, _ := toolschema.NativeBuiltinDefinition("AskUserQuestion")
	in, err := toolschema.ParseInputJSON(definition, request.Input)
	q := &questionState{id: request.ID, input: in, answers: protocol.NewJSONObject(), err: err}
	if err == nil {
		err = json.Unmarshal(in.Get("questions"), &q.questions)
		q.err = err
	}
	q.focus = make([]int, len(q.questions))
	q.selected = make([][]int, len(q.questions))
	q.editors = make([]Editor, len(q.questions))
	q.notes = make([]bool, len(q.questions))
	s.question = q
	return q
}
func (q *questionState) options() []protocol.JSONObject {
	if q.index >= len(q.questions) {
		return nil
	}
	var options []protocol.JSONObject
	_ = json.Unmarshal(q.questions[q.index].Get("options"), &options)
	return options
}
func (q *questionState) multi() bool {
	return q.index < len(q.questions) && bytesTrue(q.questions[q.index].Get("multiSelect"))
}
func bytesTrue(value json.RawMessage) bool { return string(value) == "true" }
func (q *questionState) inputFocused() bool {
	return q.index < len(q.questions) && (q.notes[q.index] || q.focus[q.index] == len(q.options()))
}
func (q *questionState) setMultiAnswer() {
	options := q.options()
	parts := []json.RawMessage{}
	for _, index := range q.selected[q.index] {
		if index < len(options) {
			parts = append(parts, options[index].Get("label"))
		}
	}
	for _, index := range q.selected[q.index] {
		if index == len(options) && q.editors[q.index].Text() != "" {
			parts = append(parts, tuiQuestionQuote(q.editors[q.index].Text()))
			break
		}
	}
	units := []uint16{}
	for i, part := range parts {
		if i > 0 {
			units = append(units, ',', ' ')
		}
		text, _ := protocol.DecodeJavaScriptJSONString(part)
		units = append(units, text...)
	}
	_ = q.answers.SetPropertyJSON(q.questions[q.index].Get("question"), protocol.QuoteJavaScriptUTF16(units))
}
func (q *questionState) toggle(index int) {
	selected := q.selected[q.index]
	found := -1
	for i, value := range selected {
		if value == index {
			found = i
			break
		}
	}
	if found < 0 {
		selected = append(selected, index)
	} else {
		selected = append(selected[:found], selected[found+1:]...)
	}
	q.selected[q.index] = selected
	q.setMultiAnswer()
}

func (q *questionState) updateOtherSelection() {
	if !q.multi() {
		return
	}
	if q.focus[q.index] == len(q.options()) && q.editors[q.index].Text() != "" {
		found := false
		for _, chosen := range q.selected[q.index] {
			found = found || chosen == len(q.options())
		}
		if !found {
			q.selected[q.index] = append(q.selected[q.index], len(q.options()))
		}
	} else if q.focus[q.index] == len(q.options()) {
		selected := []int{}
		for _, chosen := range q.selected[q.index] {
			if chosen != len(q.options()) {
				selected = append(selected, chosen)
			}
		}
		q.selected[q.index] = selected
	}
	q.setMultiAnswer()
}
func (q *questionState) setSingleAnswer() {
	options := q.options()
	index := q.focus[q.index]
	answer := tuiQuestionQuote("__other__")
	if index < len(options) {
		answer = options[index].Get("label")
	} else if q.editors[q.index].Text() != "" {
		answer = tuiQuestionQuote(q.editors[q.index].Text())
	}
	q.selected[q.index] = []int{index}
	_ = q.answers.SetPropertyJSON(q.questions[q.index].Get("question"), answer)
}
func (q *questionState) allowInput() json.RawMessage {
	next := q.input.Clone()
	next.Set("answers", tuiQuestionJSON(q.answers))
	annotations := protocol.NewJSONObject()
	for i, question := range q.questions {
		annotation := protocol.NewJSONObject()
		answer, _ := tuiQuestionOwn(q.answers, question.Get("question"))
		var options []protocol.JSONObject
		_ = json.Unmarshal(question.Get("options"), &options)
		for _, option := range options {
			if tuiQuestionEquals(option.Get("label"), answer) && option.String("preview") != "" {
				annotation.Set("preview", option.Get("preview"))
				break
			}
		}
		if notes := strings.Trim(q.editors[i].Text(), "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"); notes != "" {
			annotation.Set("notes", tuiQuestionQuote(notes))
		}
		if len(annotation.OwnProperties()) > 0 {
			_ = annotations.SetPropertyJSON(question.Get("question"), tuiQuestionJSON(annotation))
		}
	}
	if len(annotations.OwnProperties()) > 0 {
		next.Set("annotations", tuiQuestionJSON(annotations))
	}
	return tuiQuestionJSON(next)
}
func (q *questionState) feedback(finishPlan bool) json.RawMessage {
	text := "The user wants to clarify these questions.\n    This means they may have additional information, context or questions for you.\n    Take their response into account and then reformulate the questions if appropriate.\n    Start by asking them what they would like to clarify.\n\n    Questions asked:\n"
	if finishPlan {
		text = "The user has indicated they have provided enough answers for the plan interview.\nStop asking clarifying questions and proceed to finish the plan with the information you have.\n\nQuestions asked and answers provided:\n"
	}
	units := utf16.Encode([]rune(text))
	for i, question := range q.questions {
		if i > 0 {
			units = append(units, '\n')
		}
		units = append(units, '-', ' ', '"')
		name, _ := protocol.DecodeJavaScriptJSONString(question.Get("question"))
		units = append(units, name...)
		units = append(units, '"', '\n')
		answer, ok := tuiQuestionOwn(q.answers, question.Get("question"))
		if ok && string(answer) != `""` {
			units = append(units, utf16.Encode([]rune("  Answer: "))...)
			value, _ := protocol.DecodeJavaScriptJSONString(answer)
			units = append(units, value...)
		} else {
			units = append(units, utf16.Encode([]rune("  (No answer provided)"))...)
		}
	}
	return protocol.QuoteJavaScriptUTF16(units)
}

// The original UI onReject callback goes through PermissionContext's
// cancelAndAbort. Feedback continues the loop as "ask" and retains the exact
// rejection prefix; a rejection without feedback aborts the current turn.
func questionFeedbackDecision(feedback json.RawMessage) PermissionDecision {
	units, _ := protocol.DecodeJavaScriptJSONString(feedback)
	if len(units) == 0 {
		return PermissionDecision{Allowed: false, Behavior: "ask", Message: tuiQuestionQuote("The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.")}
	}
	prefix := utf16.Encode([]rune("The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\n"))
	return PermissionDecision{Allowed: false, Behavior: "ask", Message: protocol.QuoteJavaScriptUTF16(append(prefix, units...))}
}
func (s *State) closeQuestion(decision PermissionDecision) []action {
	id := s.Permissions[0].ID
	s.Permissions = s.Permissions[1:]
	s.question = nil
	s.PermissionSelected = 0
	return []action{{kind: actionPermission, permission: id, decision: decision}}
}

func (s *State) finishQuestionEditor(id string, index int, text string, changed bool) {
	if !changed || len(s.Permissions) == 0 || s.Permissions[0].ID != id || s.Permissions[0].ToolName != "AskUserQuestion" {
		return
	}
	q := s.questionFor(s.Permissions[0])
	if q.err != nil || index < 0 || index >= len(q.editors) {
		return
	}
	q.editors[index].snapshot()
	q.editors[index].Set(text)
	previous := q.index
	q.index = index
	q.updateOtherSelection()
	q.index = previous
	s.Status = ""
}
func (s *State) handleQuestionKey(key Key) []action {
	request := s.Permissions[0]
	q := s.questionFor(request)
	if key.Name == "esc" || key.Name == "escape" {
		return s.cancel()
	}
	if key.Name == "ctrl+c" {
		return s.cancel()
	}
	if q.err != nil {
		return nil
	}
	if q.index >= len(q.questions) {
		switch key.Name {
		case "left", "shift+tab":
			q.index = max(0, q.index-1)
		case "enter":
			return s.closeQuestion(PermissionDecision{Allowed: true, UpdatedInput: q.allowInput()})
		}
		return nil
	}
	options := q.options()
	chatIndex := len(options) + 1
	if q.multi() {
		chatIndex++
	}
	lastFocus := chatIndex
	if request.PermissionMode == "plan" {
		lastFocus++
	}
	focusedInput := q.inputFocused()
	if !focusedInput {
		switch key.Name {
		case "tab", "right":
			q.index = min(len(q.questions), q.index+1)
			return nil
		case "shift+tab", "left":
			q.index = max(0, q.index-1)
			return nil
		}
	}
	switch key.Name {
	case "tab":
		if focusedInput && q.multi() {
			q.focus[q.index] = min(len(options)+1, q.focus[q.index]+1)
			return nil
		}
	case "shift+tab":
		if focusedInput && q.multi() {
			q.focus[q.index] = max(0, q.focus[q.index]-1)
			return nil
		}
	case "up", "ctrl+p":
		q.focus[q.index] = max(0, q.focus[q.index]-1)
		q.notes[q.index] = false
		return nil
	case "down", "ctrl+n":
		q.focus[q.index] = min(lastFocus, q.focus[q.index]+1)
		q.notes[q.index] = false
		return nil
	case "ctrl+e":
		q.notes[q.index] = !q.notes[q.index]
		return nil
	case "space":
		if q.multi() && !focusedInput && q.focus[q.index] <= len(options) {
			q.toggle(q.focus[q.index])
			return nil
		}
	case "enter":
		if q.focus[q.index] >= chatIndex {
			return s.closeQuestion(questionFeedbackDecision(q.feedback(q.focus[q.index] == chatIndex+1)))
		}
		if q.notes[q.index] {
			q.notes[q.index] = false
			return nil
		}
		if q.multi() {
			if q.focus[q.index] == len(options)+1 {
				q.setMultiAnswer()
				q.index++
			} else {
				q.toggle(q.focus[q.index])
			}
		} else {
			q.setSingleAnswer()
			if len(q.questions) == 1 {
				return s.closeQuestion(PermissionDecision{Allowed: true, UpdatedInput: q.allowInput()})
			}
			q.index++
		}
		return nil
	case "ctrl+enter":
		if q.multi() && focusedInput {
			q.setMultiAnswer()
			q.index++
			return nil
		}
	}
	if focusedInput {
		editor := &q.editors[q.index]
		switch key.Name {
		case "left":
			editor.Left()
		case "right":
			editor.Right()
		case "home", "ctrl+a":
			editor.Home()
		case "end":
			editor.End()
		case "backspace", "ctrl+h":
			editor.Backspace()
		case "delete":
			editor.Delete()
		case "ctrl+w":
			editor.KillWordBefore()
		case "ctrl+u":
			editor.KillToStart()
		case "ctrl+k":
			editor.KillToEnd()
		case "ctrl+y":
			editor.Yank()
		case "shift+enter", "alt+enter", "ctrl+j":
			editor.Insert("\n")
		default:
			if key.Text != "" {
				editor.Insert(key.Text)
			} else if key.Name == "space" {
				editor.Insert(" ")
			}
		}
		q.updateOtherSelection()
		return nil
	}
	digit := key.Text
	if digit == "" {
		digit = key.Name
	}
	units := []rune(digit)
	if len(units) == 1 {
		n := int(units[0] - '1')
		if units[0] >= '１' && units[0] <= '９' {
			n = int(units[0] - '１')
		}
		if n >= 0 && n <= len(options) {
			q.focus[q.index] = n
			if q.multi() {
				q.toggle(n)
			} else {
				q.setSingleAnswer()
				if len(q.questions) == 1 {
					return s.closeQuestion(PermissionDecision{Allowed: true, UpdatedInput: q.allowInput()})
				}
				q.index++
			}
			return nil
		}
	}
	return nil
}
func (s *State) questionLines(request PermissionRequest, width int) []string {
	q := s.questionFor(request)
	if q.err != nil {
		return wrap("Cannot display questions: "+q.err.Error()+" · Esc: decline", width)
	}
	lines := []string{}
	headers := []string{}
	for i, question := range q.questions {
		label := question.String("header")
		if label == "" {
			label = fmt.Sprintf("Q%d", i+1)
		}
		if i == q.index {
			label = "[" + label + "]"
		}
		headers = append(headers, label)
	}
	headers = append(headers, "Submit")
	lines = append(lines, wrap(strings.Join(headers, " · "), width)...)
	if q.index >= len(q.questions) {
		for _, question := range q.questions {
			answer, ok := tuiQuestionOwn(q.answers, question.Get("question"))
			value := "(No answer provided)"
			if ok {
				value = tuiQuestionObject(tuiQuestionJSON(map[string]json.RawMessage{"value": answer})).String("value")
			}
			lines = append(lines, wrap(question.String("question")+" → "+value, width)...)
		}
		return append(lines, wrap("Enter: submit · ←/Shift+Tab: edit · Esc: decline", width)...)
	}
	question := q.questions[q.index]
	lines = append(lines, wrap(question.String("question"), width)...)
	options := q.options()
	for i, option := range options {
		prefix := "  "
		if q.focus[q.index] == i {
			prefix = "> "
		}
		mark := ""
		if q.multi() {
			mark = "[ ] "
			for _, chosen := range q.selected[q.index] {
				if chosen == i {
					mark = "[x] "
					break
				}
			}
		}
		lines = append(lines, wrap(prefix+mark+option.String("label")+" · "+option.String("description"), width)...)
	}
	prefix := "  "
	if q.focus[q.index] == len(options) {
		prefix = "> "
	}
	lines = append(lines, wrap(prefix+"Other: "+q.editors[q.index].Text(), width)...)
	if !q.multi() && q.focus[q.index] < len(options) {
		if preview := options[q.focus[q.index]].String("preview"); preview != "" {
			lines = append(lines, wrap("Preview:\n"+preview, width)...)
		}
	}
	if q.notes[q.index] {
		lines = append(lines, wrap("Notes: "+q.editors[q.index].Text(), width)...)
	}
	keys := "↑/↓: choose · Enter: answer · Tab: next · Shift+Tab: previous · Ctrl+E: notes · Esc: decline"
	if q.multi() {
		keys = "↑/↓: focus · Space/Enter: toggle · Enter on Next: continue · Tab: next question · Esc: decline"
		prefix := "  "
		if q.focus[q.index] == len(options)+1 {
			prefix = "> "
		}
		label := "Next"
		if q.index == len(q.questions)-1 {
			label = "Submit"
		}
		lines = append(lines, wrap(prefix+label, width)...)
	}
	lines = append(lines, wrap(keys, width)...)
	chatPrefix := "  "
	chatIndex := len(options) + 1
	if q.multi() {
		chatIndex++
	}
	if q.focus[q.index] == chatIndex {
		chatPrefix = "> "
	}
	lines = append(lines, wrap(chatPrefix+"Chat about this", width)...)
	if request.PermissionMode == "plan" {
		planPrefix := "  "
		if q.focus[q.index] == chatIndex+1 {
			planPrefix = "> "
		}
		lines = append(lines, wrap(planPrefix+"I'm done answering questions — finish the plan", width)...)
	}
	return lines
}
