package toolschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parseNativeInput(definition Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	if !in.IsObject() {
		return protocol.JSONObject{}, &ValidationError{Issues: array([]json.RawMessage{writeInvalidType("object", raw(in), []string{})})}
	}
	out := protocol.NewJSONObject()
	issues := []json.RawMessage{}
	known := map[string]bool{}
	for _, f := range definition.InputSchema.Fields {
		known[f.Name] = true
		v := in.Get(f.Name)
		if v == nil {
			if f.Schema.Default != nil {
				out.Set(f.Name, f.Schema.Default)
				continue
			}
			if f.Schema.Optional {
				continue
			}
		}
		if f.Schema.Semantic && len(v) > 0 && v[0] == '"' {
			var value string
			_ = json.Unmarshal(v, &value)
			switch f.Schema.Kind {
			case "boolean":
				if value == "true" || value == "false" {
					v = []byte(value)
				}
			case "number":
				if regexp.MustCompile(`^-?\d+(\.\d+)?$`).MatchString(value) {
					if n, e := strconv.ParseFloat(value, 64); e == nil && !math.IsInf(n, 0) {
						v = array(n)
					}
				}
			}
		}
		valid := true
		switch f.Schema.Kind {
		case "unknown":
			if !f.Schema.Native {
				return protocol.JSONObject{}, unsupported("registered unknown input")
			}
		case "string":
			units, e := protocol.DecodeJavaScriptJSONString(v)
			valid = e == nil
			if valid {
				issues = append(issues, nativeStringBounds(f.Schema, len(units), []any{f.Name})...)
				if f.Schema.Pattern != "" && !regexp.MustCompile(f.Schema.Pattern).MatchString(in.String(f.Name)) {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("string")}, protocol.JSONField{Name: "code", Value: quote("invalid_format")}, protocol.JSONField{Name: "format", Value: quote("regex")}, protocol.JSONField{Name: "pattern", Value: quote("/" + f.Schema.Pattern + "/")}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote("Invalid string: must match pattern /" + f.Schema.Pattern + "/")})))
				}
			}
		case "array":
			if f.Schema.Items == nil || f.Schema.Items.Kind != "string" {
				return protocol.JSONObject{}, unsupported("native array item parser")
			}
			var items []json.RawMessage
			valid = len(v) > 0 && v[0] == '[' && json.Unmarshal(v, &items) == nil
			if valid {
				for i, item := range items {
					units, e := protocol.DecodeJavaScriptJSONString(item)
					if e != nil {
						issues = append(issues, todoInvalidType("string", item, []any{f.Name, i}))
						continue
					}
					issues = append(issues, nativeStringBounds(*f.Schema.Items, len(units), []any{f.Name, i})...)
				}
				if f.Schema.MinimumItems != nil && len(items) < *f.Schema.MinimumItems {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("array")}, protocol.JSONField{Name: "code", Value: quote("too_small")}, protocol.JSONField{Name: "minimum", Value: array(*f.Schema.MinimumItems)}, protocol.JSONField{Name: "inclusive", Value: array(true)}, protocol.JSONField{Name: "path", Value: array([]any{f.Name})}, protocol.JSONField{Name: "message", Value: quote(fmt.Sprintf("Too small: expected array to have >=%d items", *f.Schema.MinimumItems))})))
				}
				if f.Schema.MaximumItems != nil && len(items) > *f.Schema.MaximumItems {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("array")}, protocol.JSONField{Name: "code", Value: quote("too_big")}, protocol.JSONField{Name: "maximum", Value: array(*f.Schema.MaximumItems)}, protocol.JSONField{Name: "inclusive", Value: array(true)}, protocol.JSONField{Name: "path", Value: array([]any{f.Name})}, protocol.JSONField{Name: "message", Value: quote(fmt.Sprintf("Too big: expected array to have <=%d items", *f.Schema.MaximumItems))})))
				}
			}
		case "boolean":
			valid = bytes.Equal(v, []byte("true")) || bytes.Equal(v, []byte("false"))
		case "number":
			var n float64
			valid = len(v) > 0 && v[0] != '"' && v[0] != 'n' && json.Unmarshal(v, &n) == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
			if valid {
				if f.Schema.Integer && math.Trunc(n) != n {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "expected", Value: quote("int")}, protocol.JSONField{Name: "format", Value: quote("safeint")}, protocol.JSONField{Name: "code", Value: quote("invalid_type")}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote("Invalid input: expected int, received number")})))
					continue
				}
				unsafe := f.Schema.Integer && math.Abs(n) > 9007199254740991
				if unsafe {
					code, bound, maximum, operator, message := "too_big", "maximum", 9007199254740991.0, "<=", "Too big"
					if n < 0 {
						code, bound, maximum, operator, message = "too_small", "minimum", -9007199254740991, ">=", "Too small"
					}
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote(code)}, protocol.JSONField{Name: bound, Value: array(maximum)}, protocol.JSONField{Name: "note", Value: quote("Integers must be within the safe integer range.")}, protocol.JSONField{Name: "origin", Value: quote("int")}, protocol.JSONField{Name: "inclusive", Value: []byte("true")}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote(message + ": expected int to be " + operator + strconv.FormatFloat(maximum, 'f', 0, 64))})))
				}
				if f.Schema.Minimum != nil && n < *f.Schema.Minimum || f.Schema.ExclusiveMinimum != nil && n <= *f.Schema.ExclusiveMinimum {
					minimum, inclusive := f.Schema.Minimum, true
					if minimum == nil {
						minimum = f.Schema.ExclusiveMinimum
						inclusive = false
					}
					operator := ">="
					if !inclusive {
						operator = ">"
					}
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("number")}, protocol.JSONField{Name: "code", Value: quote("too_small")}, protocol.JSONField{Name: "minimum", Value: array(*minimum)}, protocol.JSONField{Name: "inclusive", Value: array(inclusive)}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote("Too small: expected number to be " + operator + strconv.FormatFloat(*minimum, 'g', -1, 64))})))
					continue
				}
				if f.Schema.Maximum != nil && n > *f.Schema.Maximum {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("number")}, protocol.JSONField{Name: "code", Value: quote("too_big")}, protocol.JSONField{Name: "maximum", Value: array(*f.Schema.Maximum)}, protocol.JSONField{Name: "inclusive", Value: array(true)}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote("Too big: expected number to be <=" + strconv.FormatFloat(*f.Schema.Maximum, 'g', -1, 64))})))
					continue
				}
				if unsafe {
					continue
				}
				v = protocol.StringifyValidatedJavaScriptJSON(v)
			}
		case "enum", "literal":
			var value string
			valid = json.Unmarshal(v, &value) == nil
			values := f.Schema.Values
			if f.Schema.Kind == "literal" {
				var literal string
				_ = json.Unmarshal(f.Schema.Literal, &literal)
				values = []string{literal}
			}
			match := false
			for _, choice := range values {
				match = match || choice == value
			}
			if !match {
				choices := []string{}
				for _, choice := range values {
					choices = append(choices, string(quote(choice)))
				}
				message := "Invalid option: expected one of " + strings.Join(choices, "|")
				if len(choices) == 1 {
					message = "Invalid input: expected " + choices[0]
				}
				issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_value")}, protocol.JSONField{Name: "values", Value: array(values)}, protocol.JSONField{Name: "path", Value: array([]string{f.Name})}, protocol.JSONField{Name: "message", Value: quote(message)})))
				continue
			}
		default:
			return protocol.JSONObject{}, unsupported("native input kind " + f.Schema.Kind)
		}
		if !valid {
			issues = append(issues, writeInvalidType(f.Schema.Kind, v, []string{f.Name}))
			continue
		}
		out.Set(f.Name, v)
	}
	unknown := []json.RawMessage{}
	for _, p := range in.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		if !known[name] {
			unknown = append(unknown, p.NameJSON)
		}
	}
	if len(unknown) > 0 && definition.InputSchema.UnknownKeys != "strip" {
		word := "key"
		if len(unknown) > 1 {
			word = "keys"
		}
		units, _ := protocol.DecodeJavaScriptJSONString(quote("Unrecognized " + word + ": "))
		for i, key := range unknown {
			if i > 0 {
				units = append(units, ',', ' ')
			}
			name, _ := protocol.DecodeJavaScriptJSONString(key)
			units = append(units, '"')
			units = append(units, name...)
			units = append(units, '"')
		}
		issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("unrecognized_keys")}, protocol.JSONField{Name: "keys", Value: array(unknown)}, protocol.JSONField{Name: "path", Value: []byte("[]")}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(units)})))
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return out, nil
}

func nativeStringBounds(schema Schema, length int, path []any) []json.RawMessage {
	issues := []json.RawMessage{}
	for _, bound := range []struct {
		limit   *int
		minimum bool
	}{{schema.MinimumLength, true}, {schema.MaximumLength, false}} {
		if bound.limit == nil {
			continue
		}
		if bound.minimum && length >= *bound.limit || !bound.minimum && length <= *bound.limit {
			continue
		}
		code, key, operator, label := "too_big", "maximum", "<=", "Too big"
		if bound.minimum {
			code, key, operator, label = "too_small", "minimum", ">=", "Too small"
		}
		issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("string")}, protocol.JSONField{Name: "code", Value: quote(code)}, protocol.JSONField{Name: key, Value: array(*bound.limit)}, protocol.JSONField{Name: "inclusive", Value: array(true)}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote(fmt.Sprintf("%s: expected string to have %s%d characters", label, operator, *bound.limit))})))
	}
	return issues
}
