package sidequestion

import (
	"encoding/json"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Input struct {
	QuestionID        string
	Question, History json.RawMessage
}

var uuid = regexp.MustCompile(`(?i)^([0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

func ValidUUID(v string) bool { return uuid.MatchString(v) }
func Parse(raw json.RawMessage) (Input, error) {
	invalid := func() (Input, error) {
		return Input{}, &Error{Status: 400, Code: "BAD_REQUEST", Message: "Invalid side question: questionId UUID, nonempty question, and at most 20 history pairs are required"}
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(raw) != nil || !value.IsObject() {
		return invalid()
	}
	for _, field := range value.OwnProperties() {
		if propertyName(field.NameJSON) != "questionId" && propertyName(field.NameJSON) != "question" && propertyName(field.NameJSON) != "history" {
			return invalid()
		}
	}
	id := value.String("questionId")
	if !ValidUUID(id) {
		return invalid()
	}
	text := value.Get("question")
	if _, e := protocol.DecodeJavaScriptJSONString(text); e != nil {
		return invalid()
	}
	trimmed, e := trimJSON(text)
	if e != nil {
		return invalid()
	}
	units, _ := protocol.DecodeJavaScriptJSONString(trimmed)
	if len(units) == 0 || len(units) > 16000 {
		return invalid()
	}
	history := value.Get("history")
	if history == nil {
		history = json.RawMessage("[]")
	}
	var pairs []protocol.JSONObject
	if json.Unmarshal(history, &pairs) != nil || pairs == nil || len(pairs) > 20 {
		return invalid()
	}
	for i, pair := range pairs {
		if !pair.IsObject() {
			return invalid()
		}
		for _, field := range pair.OwnProperties() {
			if propertyName(field.NameJSON) != "question" && propertyName(field.NameJSON) != "response" {
				return invalid()
			}
		}
		q, e := trimJSON(pair.Get("question"))
		if e != nil {
			return invalid()
		}
		qu, _ := protocol.DecodeJavaScriptJSONString(q)
		ru, e := protocol.DecodeJavaScriptJSONString(pair.Get("response"))
		if len(qu) == 0 || len(qu) > 16000 || e != nil || len(ru) == 0 || len(ru) > 64000 {
			return invalid()
		}
		pairs[i].Set("question", q)
	}
	history, _ = json.Marshal(pairs)
	return Input{id, trimmed, history}, nil
}

func propertyName(raw json.RawMessage) string {
	var name string
	_ = json.Unmarshal(raw, &name)
	return name
}
func trimJSON(raw json.RawMessage) (json.RawMessage, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil, e
	}
	a, b := 0, len(units)
	for a < b && protocol.IsJavaScriptWhitespace(rune(units[a])) {
		a++
	}
	for b > a && protocol.IsJavaScriptWhitespace(rune(units[b-1])) {
		b--
	}
	return protocol.QuoteJavaScriptUTF16(units[a:b]), nil
}
