package toolschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func questionFields() []Field {
	option := Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{
		{"label", textSchema("The display text for this option that the user will see and select. Should be concise (1-5 words) and clearly describe the choice.", false)},
		{"description", textSchema("Explanation of what this option means or what will happen if chosen. Useful for providing context about trade-offs or implications.", false)},
		{"preview", textSchema("Optional preview content rendered when this option is focused. Use for mockups, code snippets, or visual comparisons that help users compare options. See the tool description for the expected content format.", true)},
	}}
	one, two, four := 1, 2, 4
	multi := Schema{Kind: "boolean", Default: json.RawMessage("false"), Description: quote("Set to true to allow the user to select multiple options instead of just one. Use when choices are not mutually exclusive.")}
	question := Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{
		{"question", textSchema(`The complete question to ask the user. Should be clear, specific, and end with a question mark. Example: "Which library should we use for date formatting?" If multiSelect is true, phrase it accordingly, e.g. "Which features do you want to enable?"`, false)},
		{"header", textSchema(`Very short label displayed as a chip/tag (max 12 chars). Examples: "Auth method", "Library", "Approach".`, false)},
		{"options", Schema{Kind: "array", Native: true, Items: &option, MinimumItems: &two, MaximumItems: &four, Description: quote(`The available choices for this question. Must have 2-4 options. Each option should be a distinct, mutually exclusive choice (unless multiSelect is enabled). There should be no 'Other' option, that will be provided automatically.`)}},
		{"multiSelect", multi},
	}}
	answer := Schema{Kind: "string"}
	annotation := Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{{"preview", textSchema("The preview content of the selected option, if the question used previews.", true)}, {"notes", textSchema("Free-text notes the user added to their selection.", true)}}}
	return []Field{
		{"questions", Schema{Kind: "array", Native: true, Items: &question, MinimumItems: &one, MaximumItems: &four, Description: quote("Questions to ask the user (1-4 questions)")}},
		{"answers", Schema{Kind: "record", Native: true, RecordValues: &answer, Optional: true, Description: quote("User answers collected by the permission component")}},
		{"annotations", Schema{Kind: "record", Native: true, RecordValues: &annotation, Optional: true, Description: quote("Optional per-question annotations from the user (e.g., notes on preview selections). Keyed by question text.")}},
		{"metadata", Schema{Kind: "object", UnknownKeys: "strip", Optional: true, Description: quote("Optional metadata for tracking and analytics purposes. Not displayed to user."), Fields: []Field{{"source", textSchema(`Optional identifier for the source of this question (e.g., "remember" for /remember command). Used for analytics tracking.`, true)}}}},
	}
}

type questionParser struct {
	issues  []json.RawMessage
	aborted bool
}

func (p *questionParser) invalid(expected string, v json.RawMessage, path []any) {
	p.issues = append(p.issues, todoInvalidType(expected, v, path))
	p.aborted = true
}
func (p *questionParser) node(schema Schema, v json.RawMessage, path []any) json.RawMessage {
	if v == nil {
		if schema.Default != nil {
			return schema.Default
		}
		if schema.Optional {
			return nil
		}
	}
	v = bytes.TrimSpace(v)
	switch schema.Kind {
	case "string":
		if _, err := protocol.DecodeJavaScriptJSONString(v); err != nil {
			p.invalid("string", v, path)
			return nil
		}
		return v
	case "boolean":
		if !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
			p.invalid("boolean", v, path)
			return nil
		}
		return v
	case "array":
		var values []json.RawMessage
		if len(v) == 0 || v[0] != '[' || json.Unmarshal(v, &values) != nil {
			p.invalid("array", v, path)
			return nil
		}
		out := []json.RawMessage{}
		for i, item := range values {
			out = append(out, p.node(*schema.Items, item, append(append([]any{}, path...), i)))
		}
		for _, bound := range []struct {
			n       *int
			minimum bool
		}{{schema.MinimumItems, true}, {schema.MaximumItems, false}} {
			if bound.n == nil || bound.minimum && len(values) >= *bound.n || !bound.minimum && len(values) <= *bound.n {
				continue
			}
			code, key, message, operator := "too_big", "maximum", "Too big", "<="
			if bound.minimum {
				code, key, message, operator = "too_small", "minimum", "Too small", ">="
			}
			p.issues = append(p.issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("array")}, protocol.JSONField{Name: "code", Value: quote(code)}, protocol.JSONField{Name: key, Value: array(*bound.n)}, protocol.JSONField{Name: "inclusive", Value: []byte("true")}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote(fmt.Sprintf("%s: expected array to have %s%d items", message, operator, *bound.n))})))
		}
		return array(out)
	case "object", "record":
		value, err := parseObject(v)
		if err != nil || !value.IsObject() {
			p.invalid(schema.Kind, v, path)
			return nil
		}
		out := protocol.NewJSONObject()
		if schema.Kind == "record" {
			for _, prop := range value.OwnProperties() {
				if string(prop.NameJSON) == `"__proto__"` {
					continue
				}
				parsed := p.node(*schema.RecordValues, prop.Value, append(append([]any{}, path...), prop.NameJSON))
				_ = out.SetPropertyJSON(prop.NameJSON, parsed)
			}
			return raw(out)
		}
		known := map[string]bool{}
		for _, field := range schema.Fields {
			known[string(quote(field.Name))] = true
			out.Set(field.Name, p.node(field.Schema, value.Get(field.Name), append(append([]any{}, path...), field.Name)))
		}
		if schema.UnknownKeys == "strict" {
			unknown := []json.RawMessage{}
			for _, prop := range value.OwnProperties() {
				if !known[string(prop.NameJSON)] {
					unknown = append(unknown, prop.NameJSON)
				}
			}
			if len(unknown) > 0 {
				word := "key"
				if len(unknown) > 1 {
					word = "keys"
				}
				units, _ := protocol.DecodeJavaScriptJSONString(quote("Unrecognized " + word + ": "))
				for i, name := range unknown {
					if i > 0 {
						units = append(units, ',', ' ')
					}
					s, _ := protocol.DecodeJavaScriptJSONString(name)
					units = append(units, '"')
					units = append(units, s...)
					units = append(units, '"')
				}
				p.issues = append(p.issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("unrecognized_keys")}, protocol.JSONField{Name: "keys", Value: array(unknown)}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(units)})))
				p.aborted = true
			}
		}
		return raw(out)
	}
	return nil
}

func parseQuestionInput(definition Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	p := questionParser{issues: []json.RawMessage{}}
	parsed := p.node(*definition.InputSchema, raw(in), []any{})
	out, _ := parseObject(parsed)
	if !p.aborted {
		var questions []protocol.JSONObject
		_ = json.Unmarshal(out.Get("questions"), &questions)
		unique := true
		seen := []json.RawMessage{}
		for _, question := range questions {
			for _, prior := range seen {
				unique = unique && !questionStringEqual(prior, question.Get("question"))
			}
			seen = append(seen, question.Get("question"))
			var options []protocol.JSONObject
			_ = json.Unmarshal(question.Get("options"), &options)
			labels := []json.RawMessage{}
			for _, option := range options {
				for _, prior := range labels {
					unique = unique && !questionStringEqual(prior, option.Get("label"))
				}
				labels = append(labels, option.Get("label"))
			}
		}
		if !unique {
			p.issues = append(p.issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("custom")}, protocol.JSONField{Name: "path", Value: []byte("[]")}, protocol.JSONField{Name: "message", Value: quote("Question texts must be unique, option labels must be unique within each question")})))
		}
	}
	if len(p.issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(p.issues)}
	}
	return out, nil
}
func questionStringEqual(a, b json.RawMessage) bool {
	x, e := protocol.DecodeJavaScriptJSONString(a)
	y, f := protocol.DecodeJavaScriptJSONString(b)
	return e == nil && f == nil && reflect.DeepEqual(x, y)
}
