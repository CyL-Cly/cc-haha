package toolschema

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
	"unicode/utf16"
)

func webFields(name string) []Field {
	if name == "WebFetch" {
		url := textSchema("The URL to fetch content from", false)
		url.Format = "uri"
		return []Field{{"url", url}, {"prompt", textSchema("The prompt to run on the fetched content", false)}}
	}
	two := 2
	query := textSchema("The search query to use", false)
	query.Native, query.MinimumLength = true, &two
	item := Schema{Kind: "string"}
	return []Field{{"query", query}, {"allowed_domains", Schema{Kind: "array", Native: true, Optional: true, Items: &item, Description: quote("Only include search results from these domains")}}, {"blocked_domains", Schema{Kind: "array", Native: true, Optional: true, Items: &item, Description: quote("Never include search results from these domains")}}}
}

func parseWebInput(def Definition, input protocol.JSONObject) (protocol.JSONObject, error) {
	// The common strict object parser supplies Source's ordered missing/type
	// and unknown-property diagnostics. URL/array/min-length refinements run at
	// the declared field position, preserving the Zod issue order.
	out := protocol.NewJSONObject()
	issues := []json.RawMessage{}
	known := map[string]bool{}
	for _, field := range def.InputSchema.Fields {
		known[field.Name] = true
		value := input.Get(field.Name)
		if value == nil && field.Schema.Optional {
			continue
		}
		if field.Schema.Kind == "array" {
			var items []json.RawMessage
			if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &items) != nil {
				issues = append(issues, writeInvalidType("array", value, []string{field.Name}))
				continue
			}
			for i, item := range items {
				if _, err := protocol.DecodeJavaScriptJSONString(item); err != nil {
					issues = append(issues, todoInvalidType("string", item, []any{field.Name, i}))
				}
			}
			out.Set(field.Name, value)
			continue
		}
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			issues = append(issues, writeInvalidType("string", value, []string{field.Name}))
			continue
		}
		if field.Name == "url" {
			if _, err := weburl.Parse(string(utf16.Decode(units))); err != nil {
				issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_format")}, protocol.JSONField{Name: "format", Value: quote("url")}, protocol.JSONField{Name: "path", Value: array([]string{"url"})}, protocol.JSONField{Name: "message", Value: quote("Invalid URL")})))
			}
		}
		if field.Name == "query" && len(units) < 2 {
			issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("string")}, protocol.JSONField{Name: "code", Value: quote("too_small")}, protocol.JSONField{Name: "minimum", Value: []byte("2")}, protocol.JSONField{Name: "inclusive", Value: []byte("true")}, protocol.JSONField{Name: "path", Value: array([]string{"query"})}, protocol.JSONField{Name: "message", Value: quote("Too small: expected string to have >=2 characters")})))
		}
		out.Set(field.Name, value)
	}
	// Reuse the strict parser's exact unknown-key diagnostic, independently of
	// field failures. Its dummy string shape has no remaining refinements.
	dummy := protocol.NewJSONObject()
	fields := []Field{}
	for _, f := range def.InputSchema.Fields {
		fields = append(fields, Field{f.Name, textSchema("", true)})
	}
	for _, p := range input.OwnProperties() {
		var k string
		_ = json.Unmarshal(p.NameJSON, &k)
		if !known[k] {
			dummy.Set(k, p.Value)
		}
	}
	_, err := parseNativeInput(Definition{InputSchema: &Schema{Kind: "object", UnknownKeys: "strict", Fields: fields}}, dummy)
	if failure, ok := err.(*ValidationError); ok {
		var extra []json.RawMessage
		_ = json.Unmarshal(failure.Issues, &extra)
		issues = append(issues, extra...)
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return out, nil
}
