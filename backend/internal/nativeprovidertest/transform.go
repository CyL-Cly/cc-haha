package nativeprovidertest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const prompt = `Say "ok" and nothing else.`

func substring(value string, length int) string {
	units := utf16.Encode([]rune(value))
	if len(units) > length {
		units = units[:length]
	}
	return string(utf16.Decode(units))
}

var completionModel = regexp.MustCompile(`(?i)^(?:o\d(?:[-.]|$)|gpt-(?:[5-9]|\d{2,})(?:[-.]|$))`)

func outputBudget(model, format string, requested int, compat protocol.JSONObject) (string, int, error) {
	if compat.String("outputTokenField") == "omit" {
		return "", 0, errors.New("This endpoint is configured to omit the output budget and cannot honor an explicit or configured output budget")
	}
	effective := requested
	var limit int
	if json.Unmarshal(compat.Get("outputTokenLimit"), &limit) == nil && effective > limit {
		effective = limit
	}
	if format == "openai_responses" {
		if compat.Get("outputTokenLimit") != nil && limit < 16 {
			return "", 0, errors.New("The Responses output budget minimum is 16, above this endpoint output limit")
		}
		return "max_output_tokens", max(16, effective), nil
	}
	override := compat.String("outputTokenField")
	if override == "max_tokens" || override == "max_completion_tokens" {
		return override, effective, nil
	}
	if completionModel.MatchString(model) {
		return "max_completion_tokens", effective, nil
	}
	return "max_tokens", effective, nil
}
func normalizeAnthropic(base string) string {
	parsed, err := weburl.Parse(base)
	if err != nil || parsed.Scheme() != "http" && parsed.Scheme() != "https" || parsed.Query() != "" || parsed.Fragment() != "" || !strings.HasSuffix(strings.TrimRight(parsed.Pathname(), "/"), "/v1") {
		return base
	}
	return regexp.MustCompile(`/v1/*$`).ReplaceAllString(base, "")
}
func buildRequest(base, key, model, format, strategy string, compat protocol.JSONObject, extra http.Header, pipeline bool) (string, http.Header, protocol.JSONObject, error) {
	headers := extra.Clone()
	headers.Set("Content-Type", "application/json")
	maxTokens := 16
	if pipeline {
		maxTokens = 64
	}
	body := record(field("model", model))
	messages := []any{map[string]any{"role": "user", "content": prompt}}
	if format == "anthropic" {
		body.Set("max_tokens", encoded(maxTokens))
		body.Set("messages", encoded(messages))
		headers.Set("anthropic-version", "2023-06-01")
		switch strategy {
		case "api_key":
			headers.Set("x-api-key", key)
		case "auth_token", "auth_token_empty_api_key":
			headers.Set("Authorization", "Bearer "+key)
		case "dual_same_token":
			headers.Set("x-api-key", key)
			headers.Set("Authorization", "Bearer "+key)
		case "dual_dummy":
			headers.Set("x-api-key", "dummy")
			headers.Set("Authorization", "Bearer dummy")
		}
		return normalizeAnthropic(base) + "/v1/messages", headers, body, nil
	}
	budget, tokens, err := outputBudget(model, format, maxTokens, compat)
	if err != nil {
		return "", nil, record(), err
	}
	headers.Set("Authorization", "Bearer "+key)
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	if format == "openai_chat" {
		if pipeline {
			body.Set("messages", encoded(messages))
			body.Set("stream", encoded(false))
			body.Set(budget, encoded(tokens))
		} else {
			body.Set(budget, encoded(tokens))
			body.Set("stream", encoded(false))
			body.Set("messages", encoded(messages))
		}
		return base + "/chat/completions", headers, body, nil
	}
	input := []any{map[string]any{"type": "message", "role": "user", "content": prompt}}
	if pipeline {
		body.Set("input", encoded(input))
		body.Set("store", encoded(false))
		body.Set(budget, encoded(tokens))
	} else {
		body.Set(budget, encoded(tokens))
		body.Set("input", encoded(input))
	}
	return base + "/responses", headers, body, nil
}

// TransformResponse shares the original non-streaming response transformation
// with the provider probe. It validates terminal evidence and tool identities.
func TransformResponse(raw json.RawMessage, model, format string, now time.Time) (protocol.JSONObject, error) {
	response := object(raw)
	if format == "openai_responses" {
		return providerproxy.ResponsesToAnthropic(response, model, providerproxy.ResponsesOptions{NowMillis: now.UnixMilli})
	}
	content := []json.RawMessage{}
	stop := "end_turn"
	if format == "openai_chat" {
		if !response.IsObject() {
			return record(), errors.New("OpenAI Chat upstream returned an invalid response")
		}
		if errorRaw := response.Get("error"); errorRaw != nil && string(errorRaw) != "null" {
			var text string
			_ = json.Unmarshal(errorRaw, &text)
			if text == "" {
				text = object(errorRaw).String("message")
			}
			if text == "" {
				text = "OpenAI Chat upstream reported an error"
			}
			return record(), errors.New(text)
		}
		if status := response.String("status"); status == "failed" || status == "cancelled" {
			return record(), errors.New("OpenAI Chat upstream response " + status)
		}
		var choices []protocol.JSONObject
		_ = json.Unmarshal(response.Get("choices"), &choices)
		if len(choices) == 0 || choices[0].Get("message") == nil || string(choices[0].Get("message")) == "null" {
			return record(), errors.New("OpenAI Chat upstream response has no message choice")
		}
		choice := choices[0]
		reason := choice.String("finish_reason")
		switch reason {
		case "stop", "content_filter":
			stop = "end_turn"
		case "tool_calls":
			stop = "tool_use"
		case "length":
			stop = "max_tokens"
		default:
			if choice.Get("finish_reason") == nil {
				reason = "undefined"
			} else if string(choice.Get("finish_reason")) == "null" {
				reason = "null"
			}
			return record(), errors.New("OpenAI Chat upstream returned missing or unknown finish_reason: " + reason)
		}
		message := object(choice.Get("message"))
		thinking := message.String("reasoning_content")
		if thinking == "" {
			thinking = message.String("reasoning")
		}
		if thinking != "" {
			content = append(content, encoded(record(field("type", "thinking"), field("thinking", thinking))))
		} else {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("thinking_blocks"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "thinking" && isString(block.Get("thinking")) {
					value := record(field("type", "thinking"), protocol.JSONField{Name: "thinking", Value: block.Get("thinking")})
					if block.Get("signature") != nil {
						value.Set("signature", block.Get("signature"))
					}
					content = append(content, encoded(value))
				}
			}
		}
		if truthy(message.Get("content")) {
			content = append(content, encoded(record(field("type", "text"), protocol.JSONField{Name: "text", Value: message.Get("content")})))
		}
		var calls []protocol.JSONObject
		_ = json.Unmarshal(message.Get("tool_calls"), &calls)
		ids := map[string]bool{}
		for _, call := range calls {
			if stop == "max_tokens" || reason == "content_filter" {
				continue
			}
			function := object(call.Get("function"))
			id, name := call.String("id"), function.String("name")
			if nativeprovidermodels.Trim(id) == "" || nativeprovidermodels.Trim(name) == "" || ids[id] {
				return record(), errors.New("OpenAI Chat tool call has missing or duplicate identity")
			}
			ids[id] = true
			input, err := toolArguments(function.Get("arguments"), false)
			if err != nil {
				return record(), err
			}
			content = append(content, encoded(record(field("type", "tool_use"), field("id", id), field("name", name), protocol.JSONField{Name: "input", Value: input})))
		}
		if stop == "tool_use" && len(ids) == 0 {
			return record(), errors.New("OpenAI Chat upstream finished with tool_calls but supplied no tool calls")
		}
	} else {
		return response, nil
	}
	if len(content) == 0 {
		content = append(content, encoded(record(field("type", "text"), field("text", ""))))
	}
	id := response.Get("id")
	if !truthy(id) {
		id = encoded(fmt.Sprintf("msg_%d", now.UnixMilli()))
	}
	used := response.Get("model")
	if !truthy(used) {
		used = encoded(model)
	}
	return record(protocol.JSONField{Name: "id", Value: id}, field("type", "message"), field("role", "assistant"), field("content", content), protocol.JSONField{Name: "model", Value: used}, field("stop_reason", stop), field("stop_sequence", nil), field("usage", usage(object(response.Get("usage"))))), nil
}
func isString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }
func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return false
	}
	if isString(raw) {
		var text string
		_ = json.Unmarshal(raw, &text)
		return text != ""
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return number != 0
	}
	return true
}
func toolArguments(raw json.RawMessage, responses bool) (json.RawMessage, error) {
	var text string
	if isString(raw) {
		_ = json.Unmarshal(raw, &text)
		if responses && nativeprovidermodels.Trim(text) == "" {
			return nil, errors.New("Invalid OpenAI Responses tool arguments: expected a JSON object")
		}
		if !responses && text == "" {
			return encoded(record()), nil
		}
		if !json.Valid([]byte(text)) {
			return protocol.CreateUnparsedToolInput(utf16.Encode([]rune(text))), nil
		}
		raw = []byte(text)
	} else if !responses && (raw == nil || string(raw) == "null") {
		return encoded(record()), nil
	}
	if !object(raw).IsObject() {
		if responses {
			return nil, errors.New("Invalid OpenAI Responses tool arguments: expected a JSON object")
		}
		return nil, errors.New("OpenAI Chat completed tool arguments must be a JSON object")
	}
	return raw, nil
}
func policyError(value protocol.JSONObject, depth int) string {
	if depth > 12 || !value.IsObject() {
		return ""
	}
	switch value.String("code") {
	case "cyber_policy", "content_policy", "content_policy_violation":
		message := value.String("message")
		if nativeprovidermodels.Trim(message) != "" {
			return message
		}
		return "Request rejected by the upstream safety policy."
	}
	for _, key := range []string{"error", "response", "originalError", "cause"} {
		if text := policyError(object(value.Get(key)), depth+1); text != "" {
			return text
		}
	}
	message := regexp.MustCompile(`^\d{3}\s+`).ReplaceAllString(value.String("message"), "")
	message = nativeprovidermodels.Trim(message)
	if strings.HasPrefix(message, "{") && json.Valid([]byte(message)) {
		return policyError(object([]byte(message)), depth+1)
	}
	return ""
}
func usage(value protocol.JSONObject) protocol.JSONObject {
	count := func(raw json.RawMessage) *float64 {
		var n float64
		if len(raw) == 0 || !bytes.HasPrefix(raw, []byte{'0'}) && !bytes.HasPrefix(raw, []byte{'1'}) && !bytes.HasPrefix(raw, []byte{'2'}) && !bytes.HasPrefix(raw, []byte{'3'}) && !bytes.HasPrefix(raw, []byte{'4'}) && !bytes.HasPrefix(raw, []byte{'5'}) && !bytes.HasPrefix(raw, []byte{'6'}) && !bytes.HasPrefix(raw, []byte{'7'}) && !bytes.HasPrefix(raw, []byte{'8'}) && !bytes.HasPrefix(raw, []byte{'9'}) {
			return nil
		}
		if json.Unmarshal(raw, &n) != nil || n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil
		}
		return &n
	}
	first := func(values ...json.RawMessage) *float64 {
		for _, v := range values {
			if n := count(v); n != nil {
				return n
			}
		}
		return nil
	}
	in := first(value.Get("input_tokens"), value.Get("prompt_tokens"))
	out := first(value.Get("output_tokens"), value.Get("completion_tokens"))
	total := count(value.Get("total_tokens"))
	if total != nil {
		if in == nil && out != nil {
			n := math.Max(0, *total-*out)
			in = &n
		} else if out == nil && in != nil {
			n := math.Max(0, *total-*in)
			out = &n
		} else if in == nil && out == nil {
			in = total
			n := 0.
			out = &n
		}
	}
	input, output := 0., 0.
	if in != nil {
		input = *in
	}
	if out != nil {
		output = *out
	}
	read, creation := 0., 0.
	if n := count(value.Get("cache_read_input_tokens")); n != nil {
		read = *n
	}
	if n := count(value.Get("cache_creation_input_tokens")); n != nil {
		creation = *n
	}
	if read == 0 && creation == 0 {
		if n := first(object(value.Get("input_tokens_details")).Get("cached_tokens"), object(value.Get("prompt_tokens_details")).Get("cached_tokens")); n != nil {
			read = math.Min(*n, input)
		}
		input = math.Max(0, input-read)
	}
	result := record(field("input_tokens", input), field("output_tokens", output))
	if read > 0 {
		result.Set("cache_read_input_tokens", encoded(read))
	}
	if creation > 0 {
		result.Set("cache_creation_input_tokens", encoded(creation))
	}
	return result
}
