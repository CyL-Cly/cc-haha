package doctorapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type field struct {
	name     string
	schema   *schema
	optional bool
}
type schema struct {
	typ           string
	nullable      bool
	enum          []string
	fields        []field
	element       *schema
	min, max      *float64
	integer, trim bool
	keyMin        int
}

func scalar(typ string) *schema { return &schema{typ: typ} }
func number(min, max float64, integer bool) *schema {
	return &schema{typ: "number", min: &min, max: &max, integer: integer}
}
func enumeration(values ...string) *schema  { return &schema{typ: "enum", enum: values} }
func object(fields ...field) *schema        { return &schema{typ: "object", fields: fields} }
func optional(name string, s *schema) field { return field{name, s, true} }
func required(name string, s *schema) field { return field{name, s, false} }
func array(element *schema) *schema         { return &schema{typ: "array", element: element} }
func mapping() *schema {
	return object(required("main", scalar("string")), optional("fable", scalar("string")), required("haiku", scalar("string")), required("sonnet", scalar("string")), required("opus", scalar("string")))
}
func nonEmpty(trim bool) *schema {
	minimum := float64(1)
	return &schema{typ: "string", min: &minimum, trim: trim}
}
func providerIndexSchema() *schema {
	compatibility := object(optional("maxOutputTokens", number(0, 9007199254740991, true)), optional("outputTokenLimit", number(0, 9007199254740991, true)), optional("outputTokenField", enumeration("auto", "max_tokens", "max_completion_tokens", "omit")), optional("sampling", enumeration("auto", "supported", "unsupported")), optional("reasoning", enumeration("auto", "supported", "unsupported")), optional("parallelTools", enumeration("auto", "supported", "unsupported")), optional("structuredOutput", enumeration("auto", "supported", "unsupported")))
	models1m := object(required("main", scalar("boolean")), optional("fable", scalar("boolean")), required("haiku", scalar("boolean")), required("sonnet", scalar("boolean")), required("opus", scalar("boolean")))
	provider := object(required("id", scalar("string")), required("presetId", scalar("string")), required("name", nonEmpty(false)), required("apiKey", scalar("string")), optional("authStrategy", enumeration("api_key", "auth_token", "auth_token_empty_api_key", "dual_same_token", "dual_dummy")), required("baseUrl", scalar("string")), optional("apiFormat", enumeration("anthropic", "openai_chat", "openai_responses")), optional("runtimeKind", enumeration("anthropic_compatible", "openai_oauth", "grok_oauth")), required("models", mapping()), optional("model1mSupport", models1m), optional("autoCompactWindow", number(16000, 10000000, true)), optional("modelContextWindows", &schema{typ: "record", element: number(16000, 10000000, true), keyMin: 1}), optional("toolSearchEnabled", scalar("boolean")), optional("disableExperimentalBetas", scalar("boolean")), optional("supportsNestedToolResultMedia", scalar("boolean")), optional("requestCompatibility", compatibility), optional("imageGeneration", object(required("model", nonEmpty(true)), optional("baseUrl", &schema{typ: "string", trim: true}), optional("apiKey", &schema{typ: "string", trim: true}))), optional("notes", scalar("string")))
	active := scalar("string")
	active.nullable = true
	return object(optional("schemaVersion", number(0, math.Inf(1), true)), required("activeId", active), required("providers", array(provider)), optional("providerOrder", array(scalar("string"))), optional("officialProviderModels", object(optional("claude-official", mapping()), optional("openai-official", mapping()), optional("grok-official", mapping()))))
}
func rawType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}
func validateProviders(raw json.RawMessage) string {
	issues := []string{}
	validate(providerIndexSchema(), raw, "", &issues)
	return strings.Join(issues, "; ")
}
func validate(s *schema, raw json.RawMessage, path string, issues *[]string) {
	add := func(message string) {
		label := path
		if label == "" {
			label = "<root>"
		}
		*issues = append(*issues, label+": "+message)
	}
	typ := rawType(raw)
	if s.nullable && typ == "null" {
		return
	}
	if s.typ == "enum" {
		var value string
		_ = json.Unmarshal(raw, &value)
		for _, option := range s.enum {
			if typ == "string" && value == option {
				return
			}
		}
		quoted := []string{}
		for _, option := range s.enum {
			quoted = append(quoted, strconv.Quote(option))
		}
		add("Invalid option: expected one of " + strings.Join(quoted, "|"))
		return
	}
	expected := s.typ
	if expected == "record" {
		expected = "object"
	}
	if typ != expected {
		add("Invalid input: expected " + expected + ", received " + typ)
		return
	}
	nested := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch s.typ {
	case "object":
		var value protocol.JSONObject
		_ = json.Unmarshal(raw, &value)
		for _, f := range s.fields {
			entry := value.Get(f.name)
			if entry == nil && f.optional {
				continue
			}
			validate(f.schema, entry, nested(f.name), issues)
		}
	case "array":
		var value []json.RawMessage
		_ = json.Unmarshal(raw, &value)
		for i, entry := range value {
			validate(s.element, entry, nested(strconv.Itoa(i)), issues)
		}
	case "record":
		var value protocol.JSONObject
		_ = json.Unmarshal(raw, &value)
		for _, property := range value.OwnProperties() {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			if len(utf16.Encode([]rune(key))) < s.keyMin {
				add("Invalid key in record")
				continue
			}
			validate(s.element, property.Value, nested(key), issues)
		}
	case "string":
		var value string
		_ = json.Unmarshal(raw, &value)
		if s.trim {
			value = trim(value)
		}
		length := float64(len(utf16.Encode([]rune(value))))
		if s.min != nil && length < *s.min {
			add(fmt.Sprintf("Too small: expected string to have >=%g characters", *s.min))
		}
	case "number":
		value, _ := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if math.IsInf(value, 0) || math.IsNaN(value) {
			add("Invalid input: expected number, received number")
			return
		}
		if s.integer && math.Trunc(value) != value {
			add("Invalid input: expected int, received number")
			return
		}
		if s.integer && (value > 9007199254740991 || value < -9007199254740991) {
			if value > 9007199254740991 {
				add("Too big: expected int to be <=9007199254740991")
			} else {
				add("Too small: expected int to be >=-9007199254740991")
			}
			return
		}
		if s.min != nil {
			if *s.min == 0 && value <= 0 {
				add("Too small: expected number to be >0")
			} else if *s.min != 0 && value < *s.min {
				add("Too small: expected number to be >=" + strconv.FormatFloat(*s.min, 'f', -1, 64))
			}
		}
		if s.max != nil && value > *s.max {
			add("Too big: expected number to be <=" + strconv.FormatFloat(*s.max, 'f', -1, 64))
		}
	}
}
