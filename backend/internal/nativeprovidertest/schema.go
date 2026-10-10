package nativeprovidertest

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func jsonType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case 'n':
		return "null"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "number"
	}
}
func ParseInput(raw json.RawMessage) (Input, []string) {
	issues := []string{}
	if jsonType(raw) != "object" {
		return Input{}, []string{"Invalid input: expected object, received " + jsonType(raw)}
	}
	o := object(raw)
	stringField := func(name string, minimum int, optional bool, trim bool) string {
		v := o.Get(name)
		if v == nil && optional {
			return ""
		}
		units, err := protocol.DecodeJavaScriptJSONString(v)
		if err != nil {
			issues = append(issues, "Invalid input: expected string, received "+jsonType(v))
			return ""
		}
		value := o.String(name)
		if trim {
			value = strings.TrimSpace(value)
		}
		if len(units) < minimum {
			issues = append(issues, "Too small: expected string to have >="+strconv.Itoa(minimum)+" characters")
		}
		return value
	}
	input := Input{BaseURL: stringField("baseUrl", 0, false, false)}
	if isString(o.Get("baseUrl")) {
		if _, err := weburl.Parse(input.BaseURL); err != nil {
			issues = append(issues, "Invalid URL")
		}
	}
	input.APIKey = stringField("apiKey", 1, false, false)
	input.ModelID = stringField("modelId", 1, false, false)
	enum := func(name, fallback string, values ...string) string {
		v := o.Get(name)
		if v == nil {
			return fallback
		}
		value := o.String(name)
		for _, allowed := range values {
			if isString(v) && value == allowed {
				return value
			}
		}
		quoted := []string{}
		for _, value := range values {
			quoted = append(quoted, strconv.Quote(value))
		}
		issues = append(issues, "Invalid option: expected one of "+strings.Join(quoted, "|"))
		return fallback
	}
	input.AuthStrategy = enum("authStrategy", "", "api_key", "auth_token", "auth_token_empty_api_key", "dual_same_token", "dual_dummy")
	input.APIFormat = enum("apiFormat", "anthropic", "anthropic", "openai_chat", "openai_responses")
	if v := o.Get("supportsNestedToolResultMedia"); v != nil {
		if jsonType(v) != "boolean" {
			issues = append(issues, "Invalid input: expected boolean, received "+jsonType(v))
		} else {
			b := string(v) == "true"
			input.SupportsNestedMedia = &b
		}
	}
	if v := o.Get("requestCompatibility"); v != nil {
		if jsonType(v) != "object" {
			issues = append(issues, "Invalid input: expected object, received "+jsonType(v))
		} else {
			input.RequestCompatibility = object(v)
			for _, name := range []string{"maxOutputTokens", "outputTokenLimit", "outputTokenField", "sampling", "reasoning", "parallelTools", "structuredOutput"} {
				value := input.RequestCompatibility.Get(name)
				if value == nil {
					continue
				}
				if name == "maxOutputTokens" || name == "outputTokenLimit" {
					if jsonType(value) != "number" {
						issues = append(issues, "Invalid input: expected number, received "+jsonType(value))
						continue
					}
					n, _ := strconv.ParseFloat(string(value), 64)
					if math.IsInf(n, 0) || math.IsNaN(n) {
						issues = append(issues, "Invalid input: expected number, received number")
					} else if math.Trunc(n) != n {
						issues = append(issues, "Invalid input: expected int, received number")
					} else if n > 9007199254740991 {
						issues = append(issues, "Too big: expected int to be <=9007199254740991")
					} else if n < -9007199254740991 {
						issues = append(issues, "Too small: expected int to be >=-9007199254740991")
					} else if n <= 0 {
						issues = append(issues, "Too small: expected number to be >0")
					}
					continue
				}
				allowed := []string{"auto", "supported", "unsupported"}
				if name == "outputTokenField" {
					allowed = []string{"auto", "max_tokens", "max_completion_tokens", "omit"}
				}
				matched := false
				for _, entry := range allowed {
					if input.RequestCompatibility.String(name) == entry && isString(value) {
						matched = true
					}
				}
				if !matched {
					quoted := []string{}
					for _, entry := range allowed {
						quoted = append(quoted, strconv.Quote(entry))
					}
					issues = append(issues, "Invalid option: expected one of "+strings.Join(quoted, "|"))
				}
			}
		}
	}
	input.PresetID = stringField("presetId", 0, true, false)
	return input, issues
}
