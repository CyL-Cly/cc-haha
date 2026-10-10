// Package pluginschema evaluates the original plugin schema declarations in
// Go. Embedded data contains types/constraints only; custom rules and dependency
// transforms are native functions, never executable JavaScript fallbacks.
package pluginschema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

//go:embed data/declarations.json
var declarations []byte

type Node struct {
	Type          string              `json:"type"`
	Shape         protocol.JSONObject `json:"shape"`
	Entries       protocol.JSONObject `json:"entries"`
	Values        []json.RawMessage   `json:"values"`
	Inner         *Node               `json:"inner"`
	InnerType     *Node               `json:"innerType"`
	Element       *Node               `json:"element"`
	KeyType       *Node               `json:"keyType"`
	ValueType     *Node               `json:"valueType"`
	In            *Node               `json:"in"`
	Out           *Node               `json:"out"`
	Catchall      *Node               `json:"catchall"`
	Options       []*Node             `json:"options"`
	Default       json.RawMessage     `json:"defaultValue"`
	Rule          string              `json:"rule"`
	Discriminator string              `json:"discriminator"`
	Checks        []Check             `json:"checks"`
}
type Check struct {
	Kind      string                        `json:"check"`
	Format    string                        `json:"format"`
	Error     string                        `json:"error"`
	Rule      string                        `json:"rule"`
	Minimum   float64                       `json:"minimum"`
	Length    int                           `json:"length"`
	Value     float64                       `json:"value"`
	Inclusive bool                          `json:"inclusive"`
	Prefix    string                        `json:"prefix"`
	Suffix    string                        `json:"suffix"`
	Pattern   struct{ Regex, Flags string } `json:"pattern"`
}
type Issue struct {
	Path    []any               `json:"path"`
	Message string              `json:"message"`
	Aborted bool                `json:"-"`
	Details protocol.JSONObject `json:"-"`
}
type Schemas struct{ Manifest, Hooks, Marketplace, Known, Installed *Node }

func Declarations() (Schemas, error) {
	var s Schemas
	err := json.Unmarshal(declarations, &s)
	return s, err
}
func Validate(schema *Node, raw json.RawMessage) (json.RawMessage, []Issue) {
	return parse(schema, raw, []any{})
}
func kind(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "undefined"
	}
	switch text[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}
func quoted(text string) json.RawMessage { raw, _ := json.Marshal(text); return raw }
func text(raw json.RawMessage) string    { var s string; _ = json.Unmarshal(raw, &s); return s }
func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	return o
}
func issue(path []any, message string) []Issue {
	return []Issue{{Path: append([]any{}, path...), Message: message, Aborted: true}}
}
func detailed(path []any, message string, aborted bool, fields ...protocol.JSONField) []Issue {
	return []Issue{{Path: append([]any{}, path...), Message: message, Aborted: aborted, Details: protocol.NewJSONObject(fields...)}}
}
func detailField(name string, value any) protocol.JSONField {
	raw, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: raw}
}
func rawJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func issueRecords(issues []Issue, prefix []any, removePrefix int) []json.RawMessage {
	records := []json.RawMessage{}
	for _, entry := range issues {
		record := entry.Details.Clone()
		if !record.IsObject() {
			record = protocol.NewJSONObject(detailField("code", "custom"))
		}
		path := entry.Path
		if len(path) >= removePrefix {
			path = path[removePrefix:]
		}
		record.Set("path", rawJSON(append(append([]any{}, prefix...), path...)))
		record.Set("message", quoted(entry.Message))
		raw, _ := record.MarshalJSON()
		records = append(records, raw)
	}
	return records
}

// ErrorJSON preserves Source's detailed issue order at Error.message.
func ErrorJSON(issues []Issue, prefix []any) string {
	raw := rawJSON(issueRecords(issues, prefix, 0))
	raw = protocol.StringifyValidatedJavaScriptJSON(raw)
	var out bytes.Buffer
	_ = json.Indent(&out, raw, "", "  ")
	return out.String()
}
func appendPath(path []any, key any) []any { return append(append([]any{}, path...), key) }
func mismatch(path []any, expected string, raw json.RawMessage) []Issue {
	return detailed(path, "Invalid input: expected "+expected+", received "+kind(raw), true, detailField("expected", expected), detailField("code", "invalid_type"))
}
func enumMessage(values []json.RawMessage) string {
	parts := []string{}
	for _, v := range values {
		parts = append(parts, string(v))
	}
	if len(parts) == 1 {
		return "Invalid input: expected " + parts[0]
	}
	return "Invalid option: expected one of " + strings.Join(parts, "|")
}
func parse(s *Node, raw json.RawMessage, path []any) (json.RawMessage, []Issue) {
	if s == nil {
		return raw, nil
	}
	switch s.Type {
	case "optional":
		if len(raw) == 0 {
			return nil, nil
		}
		return parse(s.InnerType, raw, path)
	case "default":
		if len(raw) == 0 {
			return s.Default, nil
		}
		return parse(s.InnerType, raw, path)
	case "lazy":
		return parse(s.Inner, raw, path)
	case "pipe":
		value, errors := parse(s.In, raw, path)
		if len(errors) > 0 {
			return nil, errors
		}
		return parse(s.Out, value, path)
	case "transform":
		if s.Rule == "dependency-string" {
			v := text(raw)
			if i := strings.LastIndex(v, "@^"); i >= 0 {
				v = v[:i]
			}
			return quoted(v), nil
		}
		if s.Rule == "dependency-object" {
			o := object(raw)
			name := o.String("name")
			if market := o.String("marketplace"); market != "" {
				name += "@" + market
			}
			return quoted(name), nil
		}
		return nil, issue(path, "Unimplemented native schema transform")
	case "unknown":
		return raw, nil
	case "never":
		return nil, mismatch(path, "never", raw)
	case "union":
		if s.Discriminator != "" {
			if kind(raw) != "object" {
				return nil, mismatch(path, "object", raw)
			}
			discriminator := object(raw).Get(s.Discriminator)
			for _, option := range s.Options {
				var field Node
				_ = json.Unmarshal(option.Shape.Get(s.Discriminator), &field)
				for _, value := range field.Values {
					if equal(value, discriminator) {
						return parse(option, raw, path)
					}
				}
			}
			return nil, detailed(appendPath(path, s.Discriminator), "Invalid input", true, detailField("code", "invalid_union"), detailField("errors", []any{}), detailField("note", "No matching discriminator"), detailField("discriminator", s.Discriminator))
		}
		dirty := [][]Issue{}
		branches := [][]Issue{}
		for _, option := range s.Options {
			value, errors := parse(option, raw, path)
			if len(errors) == 0 {
				return value, nil
			}
			// A pipeline cannot run its transform on any failed input, including
			// continuable refinements; Source marks that branch aborted.
			aborted := option.Type == "pipe"
			for _, e := range errors {
				if e.Aborted {
					aborted = true
				}
			}
			branches = append(branches, errors)
			if !aborted {
				dirty = append(dirty, errors)
			}
		}
		if len(dirty) == 1 {
			return nil, dirty[0]
		}
		branchJSON := []json.RawMessage{}
		for _, branch := range branches {
			branchJSON = append(branchJSON, rawJSON(issueRecords(branch, nil, len(path))))
		}
		return nil, detailed(path, "Invalid input", true, detailField("code", "invalid_union"), detailField("errors", branchJSON))
	case "literal":
		for _, value := range s.Values {
			if equal(value, raw) {
				return raw, nil
			}
		}
		return nil, detailed(path, enumMessage(s.Values), true, detailField("code", "invalid_value"), detailField("values", s.Values))
	case "enum":
		values := []json.RawMessage{}
		for _, entry := range s.Entries.OwnProperties() {
			values = append(values, entry.Value)
			if equal(entry.Value, raw) {
				return raw, nil
			}
		}
		return nil, detailed(path, enumMessage(values), true, detailField("code", "invalid_value"), detailField("values", values))
	case "object":
		if kind(raw) != "object" {
			return nil, mismatch(path, "object", raw)
		}
		in, out := object(raw), protocol.NewJSONObject()
		errs := []Issue{}
		known := map[string]bool{}
		for _, property := range s.Shape.OwnProperties() {
			name := text(property.NameJSON)
			known[name] = true
			var child Node
			_ = json.Unmarshal(property.Value, &child)
			value, e := parse(&child, in.Get(name), appendPath(path, name))
			errs = append(errs, e...)
			out.Set(name, value)
		}
		extra := []string{}
		for _, property := range in.OwnProperties() {
			name := text(property.NameJSON)
			if known[name] {
				continue
			}
			if s.Catchall != nil {
				if s.Catchall.Type == "never" {
					extra = append(extra, name)
				} else {
					value, e := parse(s.Catchall, property.Value, appendPath(path, name))
					errs = append(errs, e...)
					_ = out.SetPropertyJSON(property.NameJSON, value)
				}
			}
		}
		if len(extra) > 0 {
			parts := []string{}
			for _, key := range extra {
				parts = append(parts, string(quoted(key)))
			}
			label := "Unrecognized key: "
			if len(extra) > 1 {
				label = "Unrecognized keys: "
			}
			errs = append(errs, detailed(path, label+strings.Join(parts, ", "), true, detailField("code", "unrecognized_keys"), detailField("keys", extra))...)
		}
		encoded, _ := out.MarshalJSON()
		return applyChecks(s, encoded, path, errs)
	case "record":
		if kind(raw) != "object" {
			return nil, mismatch(path, "record", raw)
		}
		out := protocol.NewJSONObject()
		errs := []Issue{}
		for _, property := range object(raw).OwnProperties() {
			name := text(property.NameJSON)
			// Zod record parsing skips this key before validating its value.
			// Source does not turn it into an own output property.
			if name == "__proto__" {
				continue
			}
			key, e := parse(s.KeyType, property.NameJSON, []any{})
			if len(e) > 0 {
				errs = append(errs, detailed(appendPath(path, name), "Invalid key in record", true,
					detailField("code", "invalid_key"), detailField("origin", "record"),
					detailField("issues", issueRecords(e, nil, 0)))...)
				continue
			}
			value, e := parse(s.ValueType, property.Value, appendPath(path, name))
			errs = append(errs, e...)
			_ = out.SetPropertyJSON(key, value)
		}
		encoded, _ := out.MarshalJSON()
		return applyChecks(s, encoded, path, errs)
	case "array":
		if kind(raw) != "array" {
			return nil, mismatch(path, "array", raw)
		}
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		out := []json.RawMessage{}
		errs := []Issue{}
		for i, value := range values {
			v, e := parse(s.Element, value, appendPath(path, i))
			errs = append(errs, e...)
			out = append(out, v)
		}
		encoded, _ := json.Marshal(out)
		return applyChecks(s, encoded, path, errs)
	case "string", "number", "boolean":
		if kind(raw) != s.Type {
			return nil, mismatch(path, s.Type, raw)
		}
		if s.Type == "number" {
			value, e := strconv.ParseFloat(string(raw), 64)
			if e != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return nil, mismatch(path, "number", raw)
			}
		}
		return applyChecks(s, raw, path, nil)
	}
	return nil, issue(path, "Unimplemented native schema type: "+s.Type)
}
func equal(a, b json.RawMessage) bool {
	// Missing object properties represent JavaScript undefined, not validated
	// JSON. Compare presence before invoking the JSON normalizer.
	return len(a) > 0 && len(b) > 0 && string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
func applyChecks(s *Node, raw json.RawMessage, path []any, errs []Issue) (json.RawMessage, []Issue) {
	if len(errs) > 0 {
		return raw, errs
	}
	for _, check := range s.Checks {
		ok := true
		message := "Invalid input"
		value := text(raw)
		switch check.Kind {
		case "custom":
			ok = custom(check.Rule, raw)
		case "min_length", "length_equals":
			length := 0
			if kind(raw) == "string" {
				units, _ := protocol.DecodeJavaScriptJSONString(raw)
				length = len(units)
			} else {
				var values []json.RawMessage
				_ = json.Unmarshal(raw, &values)
				length = len(values)
			}
			noun := "characters"
			if kind(raw) == "array" {
				noun = "items"
			}
			if check.Kind == "min_length" {
				ok = float64(length) >= check.Minimum
				message = fmt.Sprintf("Too small: expected %s to have >=%g %s", kind(raw), check.Minimum, noun)
			} else {
				ok = length == check.Length
				message = fmt.Sprintf("Too %s: expected %s to have %s%d %s", map[bool]string{true: "small", false: "big"}[length < check.Length], kind(raw), map[bool]string{true: ">=", false: "<="}[length < check.Length], check.Length, noun)
			}
		case "greater_than":
			number, _ := strconv.ParseFloat(string(raw), 64)
			ok = number > check.Value
			if check.Inclusive {
				ok = number >= check.Value
			}
			operator := ">"
			if check.Inclusive {
				operator = ">="
			}
			message = fmt.Sprintf("Too small: expected number to be %s%g", operator, check.Value)
		case "number_format":
			number, _ := strconv.ParseFloat(string(raw), 64)
			ok = math.Trunc(number) == number && math.Abs(number) <= 9007199254740991
			message = "Invalid input: expected int, received number"
		case "string_format":
			switch check.Format {
			case "url":
				_, e := whatwg.NewParser().Parse(value)
				ok = e == nil
				message = "Invalid URL"
			case "starts_with":
				ok = strings.HasPrefix(value, check.Prefix)
				message = "Invalid string: must start with " + string(quoted(check.Prefix))
			case "ends_with":
				ok = strings.HasSuffix(value, check.Suffix)
				message = "Invalid string: must end with " + string(quoted(check.Suffix))
			case "regex":
				options := regexp2.ECMAScript
				if strings.Contains(check.Pattern.Flags, "i") {
					options |= regexp2.IgnoreCase
				}
				pattern, e := regexp2.Compile(check.Pattern.Regex, options)
				ok = false
				if e == nil {
					ok, _ = pattern.MatchString(value)
				}
				message = "Invalid string: must match pattern /" + check.Pattern.Regex + "/" + check.Pattern.Flags
			}
		}
		if !ok {
			if check.Error != "" {
				message = check.Error
			}
			item := detailed(path, message, false, detailField("code", "custom"))
			if check.Kind == "string_format" {
				fields := []protocol.JSONField{detailField("origin", "string"), detailField("code", "invalid_format"), detailField("format", check.Format)}
				switch check.Format {
				case "starts_with":
					fields = append(fields, detailField("prefix", check.Prefix))
				case "ends_with":
					fields = append(fields, detailField("suffix", check.Suffix))
				case "regex":
					fields = append(fields, detailField("pattern", "/"+check.Pattern.Regex+"/"+check.Pattern.Flags))
				}
				item = detailed(path, message, false, fields...)
			}
			if check.Kind == "greater_than" {
				item = detailed(path, message, false, detailField("origin", "number"), detailField("code", "too_small"), detailField("minimum", check.Value), detailField("inclusive", check.Inclusive))
			}
			if check.Kind == "min_length" {
				item = detailed(path, message, false, detailField("origin", kind(raw)), detailField("code", "too_small"), detailField("minimum", check.Minimum), detailField("inclusive", true))
			}
			if check.Kind == "number_format" {
				item = detailed(path, message, true, detailField("expected", "int"), detailField("format", "safeint"), detailField("code", "invalid_type"))
			}
			errs = append(errs, item...)
		}
	}
	return raw, errs
}
func custom(rule string, raw json.RawMessage) bool {
	value := text(raw)
	switch rule {
	case "no-spaces":
		return !strings.Contains(value, " ")
	case "command-exclusive":
		o := object(raw)
		return (o.String("source") != "") != (o.String("content") != "")
	case "mcpb-extension":
		return strings.HasSuffix(value, ".mcpb") || strings.HasSuffix(value, ".dxt")
	case "lsp-command":
		return !strings.Contains(value, " ") || strings.HasPrefix(value, "/")
	case "nonempty-record":
		return len(object(raw).OwnProperties()) > 0
	case "file-extension":
		return strings.HasPrefix(value, ".")
	case "marketplace-path":
		return !strings.ContainsAny(value, `/\`) && !strings.Contains(value, "..") && value != "."
	case "not-inline":
		return strings.ToLower(value) != "inline"
	case "not-builtin":
		return strings.ToLower(value) != "builtin"
	case "npm-traversal":
		return !strings.Contains(value, "..") && !strings.Contains(value, "//")
	case "npm-name":
		return regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9-._]*/)?[a-z0-9][a-z0-9-._]*$`).MatchString(value)
	case "remote-settings-source":
		return kind(object(raw).Get("source")) != "string"
	case "marketplace-official":
		return !blockedOfficial(value)
	case "not-reserved-marketplace":
		return !reserved[strings.ToLower(value)]
	}
	return false
}

var reserved = map[string]bool{"claude-code-marketplace": true, "claude-code-plugins": true, "claude-plugins-official": true, "anthropic-marketplace": true, "anthropic-plugins": true, "agent-skills": true, "life-sciences": true, "knowledge-work-plugins": true}
var officialPattern = regexp.MustCompile(`(?i)(?:official[^a-z0-9]*(anthropic|claude)|(?:anthropic|claude)[^a-z0-9]*official|^(?:anthropic|claude)[^a-z0-9]*(marketplace|plugins|official))`)

func blockedOfficial(name string) bool {
	if reserved[strings.ToLower(name)] {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r > 0x7e {
			return true
		}
	}
	return officialPattern.MatchString(name)
}
