package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (s *Service) startQuestion(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	future, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if err != nil {
		return nil, err
	}
	if err = future.Resolve(scope, queryloop.ToolCallResult{Data: questionCall(in)}); err != nil {
		return nil, err
	}
	return future, nil
}

func questionObject(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(value, &out)
	return out
}
func questionTruthy(value json.RawMessage) bool {
	var out any
	if json.Unmarshal(value, &out) != nil {
		return false
	}
	switch out := out.(type) {
	case nil:
		return false
	case string:
		return out != ""
	case bool:
		return out
	case float64:
		return out != 0
	}
	return true
}
func questionCall(in protocol.JSONObject) json.RawMessage {
	answers := in.Get("answers")
	if answers == nil {
		answers = []byte("{}")
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "questions", Value: in.Get("questions")}, protocol.JSONField{Name: "answers", Value: answers})
	if boolean(questionObject(in.Get("metadata")), "autoAnswered", false) {
		out.Set("selectionSource", quote("automatic"))
	}
	if annotations := in.Get("annotations"); questionTruthy(annotations) {
		out.Set("annotations", annotations)
	}
	return raw(out)
}

func validateQuestionHTML(in protocol.JSONObject, format string) error {
	if format != "html" {
		return nil
	}
	var questions []protocol.JSONObject
	_ = json.Unmarshal(in.Get("questions"), &questions)
	for _, question := range questions {
		var options []protocol.JSONObject
		_ = json.Unmarshal(question.Get("options"), &options)
		for _, option := range options {
			if option.Get("preview") == nil {
				continue
			}
			preview := option.String("preview")
			message := ""
			space := `[\s\x{000b}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*`
			switch {
			case regexp.MustCompile(`<` + space + `(?i:html|body|!doctype)\b`).MatchString(preview):
				message = "preview must be an HTML fragment, not a full document (no <html>, <body>, or <!DOCTYPE>)"
			case regexp.MustCompile(`<` + space + `(?i:script|style)\b`).MatchString(preview):
				message = "preview must not contain <script> or <style> tags. Use inline styles via the style attribute if needed."
			case !regexp.MustCompile(`<[a-zA-Z][^>]*>`).MatchString(preview):
				message = `preview must contain HTML (previewFormat is set to "html"). Wrap content in a tag like <div> or <pre>.`
			}
			if message != "" {
				return errors.New(`Option "` + option.String("label") + `" in question "` + question.String("question") + `": ` + message)
			}
		}
	}
	return nil
}

func questionUnits(value json.RawMessage) []uint16 {
	u, _ := protocol.DecodeJavaScriptJSONString(value)
	return u
}
func equalQuestion(a, b json.RawMessage) bool {
	return reflect.DeepEqual(questionUnits(a), questionUnits(b))
}
func questionOwn(value protocol.JSONObject, name json.RawMessage) (json.RawMessage, bool) {
	for _, prop := range value.OwnProperties() {
		if equalQuestion(prop.NameJSON, name) {
			return prop.Value, true
		}
	}
	return nil, false
}
func trimQuestionUnits(units []uint16) []uint16 {
	space := func(v uint16) bool {
		switch v {
		case 9, 10, 11, 12, 13, 32, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
			return true
		}
		return v >= 0x2000 && v <= 0x200a
	}
	for len(units) > 0 && space(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && space(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return units
}
func mapQuestion(out protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	var questions []protocol.JSONObject
	if err := json.Unmarshal(out.Get("questions"), &questions); err != nil {
		return protocol.JSONObject{}, err
	}
	answers := questionObject(out.Get("answers"))
	annotations := questionObject(out.Get("annotations"))
	content := []uint16{}
	appendText := func(v string) { content = append(content, utf16.Encode([]rune(v))...) }
	automatic := out.String("selectionSource") == "automatic"
	if automatic {
		appendText("Answers were selected automatically after the timeout: ")
	} else {
		appendText("User has answered your questions: ")
	}
	allAnswered, onlyOptions, noNotes := true, true, true
	for _, question := range questions {
		_, ok := questionOwn(answers, question.Get("question"))
		allAnswered = allAnswered && ok
	}
	for _, annotation := range annotations.OwnProperties() {
		notes := questionObject(annotation.Value).Get("notes")
		if len(trimQuestionUnits(questionUnits(notes))) > 0 {
			noNotes = false
		}
	}
	for i, answer := range answers.OwnProperties() {
		if i > 0 {
			appendText(", ")
		}
		appendText(`"`)
		content = append(content, questionUnits(answer.NameJSON)...)
		appendText(`"="`)
		content = append(content, questionUnits(answer.Value)...)
		appendText(`"`)
		annotation, _ := questionOwn(annotations, answer.NameJSON)
		details := questionObject(annotation)
		if v := details.Get("preview"); questionTruthy(v) {
			appendText(" selected preview:\n")
			content = append(content, questionUnits(v)...)
		}
		if v := details.Get("notes"); questionTruthy(v) {
			appendText(" user notes: ")
			content = append(content, questionUnits(v)...)
		}
		var question *protocol.JSONObject
		for j := range questions {
			if equalQuestion(questions[j].Get("question"), answer.NameJSON) {
				question = &questions[j]
				break
			}
		}
		if question == nil {
			onlyOptions = false
			continue
		}
		labels := [][]uint16{questionUnits(answer.Value)}
		if boolean(*question, "multiSelect", false) {
			labels = nil
			remaining := questionUnits(answer.Value)
			start := 0
			for j, unit := range remaining {
				if unit == ',' {
					part := trimQuestionUnits(remaining[start:j])
					if len(part) > 0 {
						labels = append(labels, part)
					}
					start = j + 1
				}
			}
			part := trimQuestionUnits(remaining[start:])
			if len(part) > 0 {
				labels = append(labels, part)
			}
		}
		if len(labels) == 0 {
			onlyOptions = false
		}
		var options []protocol.JSONObject
		_ = json.Unmarshal(question.Get("options"), &options)
		for _, label := range labels {
			found := false
			for _, option := range options {
				found = found || reflect.DeepEqual(label, questionUnits(option.Get("label")))
			}
			onlyOptions = onlyOptions && found
		}
	}
	appendText(". ")
	if automatic {
		appendText("These are automatic selections, not direct user input. Use them as defaults where appropriate; do not treat them as approval for irreversible actions.")
	} else if allAnswered && onlyOptions && noNotes {
		appendText("You can now continue with the user's answers in mind.")
	} else {
		appendText("Carefully consider the user's answers before proceeding. The answers may require that you pause, clarify, or change your approach.")
	}
	result := protocol.NewJSONObject(field("type", "tool_result"), protocol.JSONField{Name: "content", Value: protocol.QuoteJavaScriptUTF16(content)}, protocol.JSONField{Name: "tool_use_id", Value: id})
	return result, nil
}
