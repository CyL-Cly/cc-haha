package agentsapi

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type MutationContext struct{ Scope, CWD, Target string }
type Mutation struct {
	Fields  protocol.JSONObject
	Context MutationContext
}
type APIError = settings.APIError

func fail(status int, code, message string) error {
	return &APIError{StatusCode: status, Code: code, Message: message}
}
func bad(message string) error { return fail(400, "BAD_REQUEST", message) }

var slug = regexp.MustCompile("^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$")

func ValidateName(name string) error {
	if !slug.MatchString(name) {
		return bad("Agent name must be a lowercase slug using only letters, numbers, \"-\", or \"_\" (max 64 characters)")
	}
	return nil
}
func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func jsString(raw json.RawMessage) (string, bool) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	return string(utf16.Decode(units)), err == nil
}
func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func required(raw json.RawMessage, field string) (json.RawMessage, error) {
	text, ok := jsString(raw)
	if !ok || trim(text) == "" {
		return nil, bad("Agent " + field + " must be a non-empty string")
	}
	return quote(trim(text)), nil
}
func parseBody(raw []byte) (protocol.JSONObject, error) {
	if !json.Valid(raw) {
		return protocol.JSONObject{}, bad("Invalid JSON body")
	}
	var body protocol.JSONObject
	if body.UnmarshalJSON(raw) != nil || !body.IsObject() {
		return protocol.JSONObject{}, bad("JSON body must be an object")
	}
	return body, nil
}
func allowed(body protocol.JSONObject, fields []string) error {
	set := map[string]bool{}
	for _, name := range fields {
		set[name] = true
	}
	for _, field := range body.OwnProperties() {
		name, _ := jsString(field.NameJSON)
		if !set[name] && name != "" {
			return bad("Unexpected agent field: " + name)
		}
	}
	return nil
}

var createFields = []string{"scope", "cwd", "name", "description", "systemPrompt", "model", "effort", "tools", "color"}
var overrideFields = []string{"model", "effort", "cwd"}

func scope(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("\"\"")) {
		return "user", nil
	}
	value, ok := jsString(raw)
	if ok && (value == "user" || value == "project") {
		return value, nil
	}
	return "", bad("Agent scope must be \"user\" or \"project\"")
}
func ParseMutation(raw []byte, update bool, name string) (Mutation, error) {
	body, err := parseBody(raw)
	if err != nil {
		return Mutation{}, err
	}
	fields := createFields
	if update {
		fields = append(append([]string{}, fields...), "target")
	}
	if err = allowed(body, fields); err != nil {
		return Mutation{}, err
	}
	context := MutationContext{}
	context.Scope, err = scope(body.Get("scope"))
	if err != nil {
		return Mutation{}, err
	}
	if value := body.Get("cwd"); value != nil {
		context.CWD, _ = jsString(value)
		if _, ok := jsString(value); !ok {
			return Mutation{}, bad("Agent cwd must be a string")
		}
	}
	if update {
		if value := body.Get("target"); value != nil {
			target, ok := jsString(value)
			if !ok || trim(target) == "" {
				return Mutation{}, bad("Agent target must be a non-empty string")
			}
			context.Target = target
		}
	}
	out := protocol.NewJSONObject()
	if !update {
		for _, field := range []string{"name", "description", "systemPrompt"} {
			value, err := required(body.Get(field), field)
			if err != nil {
				return Mutation{}, err
			}
			out.Set(field, value)
			if field == "name" {
				if err = ValidateName(out.String("name")); err != nil {
					return Mutation{}, err
				}
			}
		}
	}
	optional, err := parseOptional(body, update)
	if err != nil {
		return Mutation{}, err
	}
	for _, field := range optional.OwnProperties() {
		name, _ := jsString(field.NameJSON)
		out.Set(name, field.Value)
	}
	if update {
		if value := body.Get("name"); value != nil {
			value, err = required(value, "name")
			if err != nil {
				return Mutation{}, err
			}
			text, _ := jsString(value)
			if err = ValidateName(text); err != nil {
				return Mutation{}, err
			}
			out.Set("name", value)
			if text != name {
				return Mutation{}, bad("Agent name cannot be changed")
			}
		}
		if value := body.Get("description"); value != nil {
			value, err = required(value, "description")
			if err != nil {
				return Mutation{}, err
			}
			out.Set("description", value)
		}
		if value := body.Get("systemPrompt"); value != nil {
			text, ok := jsString(value)
			if !ok {
				return Mutation{}, bad("Agent systemPrompt must be a string")
			}
			out.Set("systemPrompt", quote(trim(text)))
		}
	}
	return Mutation{out, context}, nil
}
func parseOptional(body protocol.JSONObject, allowNull bool) (protocol.JSONObject, error) {
	out := protocol.NewJSONObject()
	for _, name := range []string{"model", "effort", "tools", "color"} {
		value := body.Get(name)
		if value == nil {
			continue
		}
		if bytes.Equal(value, []byte("null")) {
			if allowNull {
				out.Set(name, value)
			}
			continue
		}
		switch name {
		case "effort":
			value = ParseEffort(value)
			if value == nil {
				return out, bad("Agent effort must be a supported level or integer")
			}
		case "tools":
			var rows []json.RawMessage
			if json.Unmarshal(value, &rows) != nil || len(value) == 0 || value[0] != '[' {
				return out, bad("Agent tools must be an array of non-empty strings")
			}
			tools := []json.RawMessage{}
			for _, row := range rows {
				text, ok := jsString(row)
				if !ok || trim(text) == "" {
					return out, bad("Agent tools must be an array of non-empty strings")
				}
				tools = append(tools, quote(trim(text)))
			}
			value, _ = json.Marshal(tools)
		default:
			var err error
			value, err = required(value, name)
			if err != nil {
				return out, err
			}
			if name == "color" {
				text, _ := jsString(value)
				valid := false
				for _, color := range []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"} {
					if text == color {
						valid = true
						break
					}
				}
				if !valid {
					return out, bad("Unsupported agent color: " + text)
				}
			}
		}
		out.Set(name, value)
	}
	return out, nil
}

var integerEffort = regexp.MustCompile("^-?[0-9]+$")

func ParseEffort(raw json.RawMessage) json.RawMessage {
	if raw == nil || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("\"\"")) {
		return nil
	}
	number := func(text string) json.RawMessage {
		n, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || math.Trunc(n) != n {
			return nil
		}
		value, _ := json.Marshal(n)
		return protocol.StringifyValidatedJavaScriptJSON(value)
	}
	if len(raw) > 0 && raw[0] != '"' && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		if result := number(string(raw)); result != nil {
			return result
		}
	}
	coerced, err := protocol.JavaScriptStringJSON(raw)
	if err != nil {
		return nil
	}
	value, _ := jsString(coerced)
	value = strings.ToLower(trim(value))
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		if value == level {
			return quote(value)
		}
	}
	if !integerEffort.MatchString(value) {
		return nil
	}
	return number(value)
}
func ParseOverride(raw []byte) (protocol.JSONObject, string, error) {
	body, err := parseBody(raw)
	if err != nil {
		return protocol.JSONObject{}, "", err
	}
	if err = allowed(body, overrideFields); err != nil {
		return protocol.JSONObject{}, "", err
	}
	out := protocol.NewJSONObject()
	if value := body.Get("model"); value != nil {
		if !bytes.Equal(value, []byte("null")) {
			value, err = required(value, "model")
			if err != nil {
				return out, "", err
			}
		}
		out.Set("model", value)
	}
	if value := body.Get("effort"); value != nil {
		if !bytes.Equal(value, []byte("null")) {
			value = ParseEffort(value)
			if value == nil {
				return out, "", bad("Agent effort must be a supported level or integer")
			}
		}
		out.Set("effort", value)
	}
	cwd, _ := jsString(body.Get("cwd"))
	return out, cwd, nil
}
