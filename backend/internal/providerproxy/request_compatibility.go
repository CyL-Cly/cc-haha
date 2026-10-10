package providerproxy

import (
	"encoding/json"
	"math"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type RequestCompatibilityError struct{ Message string }

func (e *RequestCompatibilityError) Error() string { return e.Message }
func (e *RequestCompatibilityError) Status() int   { return 400 }
func (e *RequestCompatibilityError) Type() string  { return "invalid_request_error" }

func requestCompatibilityFailure(message string) error { return &RequestCompatibilityError{message} }

var requestCompletionFamily = regexp.MustCompile(`(?i)^(?:o\d(?:[-.]|$)|gpt-(?:[5-9]|\d{2,})(?:[-.]|$))`)

func ResolveRequestCompatibility(body, options protocol.JSONObject) (protocol.JSONObject, error) {
	compat := requestObject(options.Get("requestCompatibility"))
	source := requestFallback(options.Get("budgetSource"), requestEncode("explicit"))
	requested := body.Get("max_tokens")
	value := requestNumber(requested)
	oauth := requestTruthy(options.Get("openAICodexOAuth"))
	if !oauth && (requested == nil || requestIsString(requested) || string(requested) == "null" || string(requested) == "true" || string(requested) == "false" || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value <= 0 || value > 9007199254740991) {
		return requestRecord(), requestCompatibilityFailure("The output budget must be a positive integer")
	}
	hard := compat.Get("outputTokenLimit")
	configured := json.RawMessage(nil)
	if string(source) == `"default"` {
		configured = compat.Get("maxOutputTokens")
	}
	budget := requestRecord(protocol.JSONField{Name: "source", Value: source}, protocol.JSONField{Name: "requested", Value: requested}, requestField("field", "omit"), requestField("reason", "upstream_default"))
	budget.Set("configured", configured)
	budget.Set("hardLimit", hard)
	if oauth {
		budget.Set("reason", requestEncode("oauth_omit"))
	} else if compat.String("outputTokenField") == "omit" {
		if string(source) == `"explicit"` || configured != nil || hard != nil {
			return requestRecord(), requestCompatibilityFailure("This endpoint is configured to omit the output budget and cannot honor an explicit or configured output budget")
		}
		budget.Set("reason", requestEncode("provider_omit"))
	} else if string(source) == `"explicit"` || configured != nil || hard != nil {
		effective := configured
		reason := "provider_default"
		if configured == nil || string(configured) == "null" {
			if string(source) == `"default"` {
				effective = hard
				if configured == nil {
					reason = "hard_limit"
				}
			} else {
				effective = requested
				if configured == nil {
					reason = "explicit_request"
				}
			}
		}
		if hard != nil && requestNumber(effective) > requestNumber(hard) {
			effective = hard
			reason = "hard_limit"
		}
		field := compat.String("outputTokenField")
		if options.String("protocol") == "openai_responses" {
			if hard != nil && requestNumber(hard) < 16 {
				return requestRecord(), requestCompatibilityFailure("The Responses output budget minimum is 16, above this endpoint output limit")
			}
			if requestNumber(effective) < 16 {
				effective = requestEncode(16)
				reason = "responses_minimum"
			}
			field = "max_output_tokens"
		} else if field != "max_tokens" && field != "max_completion_tokens" {
			field = "max_tokens"
			if requestCompletionFamily.MatchString(body.String("model")) {
				field = "max_completion_tokens"
			}
		}
		budget.Set("field", requestEncode(field))
		budget.Set("reason", requestEncode(reason))
		budget.Set("effective", effective)
	}
	tools := []protocol.JSONObject{}
	for _, raw := range requestArray(body.Get("tools")) {
		value := requestObject(raw)
		if value.String("name") != "BatchTool" {
			tools = append(tools, value)
		}
	}
	choice := requestObject(body.Get("tool_choice"))
	named := choice.String("type") != "tool"
	for _, tool := range tools {
		if string(tool.Get("name")) == string(choice.Get("name")) {
			named = true
		}
	}
	parallel := json.RawMessage(nil)
	disable := choice.Get("disable_parallel_tool_use")
	if len(tools) > 0 && named && (string(disable) == "true" || string(disable) == "false") {
		if compat.String("parallelTools") == "unsupported" {
			if string(disable) == "true" {
				return requestRecord(), requestCompatibilityFailure("This endpoint cannot honor the serial tool-call constraint")
			}
		} else {
			parallel = requestEncode(string(disable) != "true")
		}
	}
	formatRaw := requestFallback(requestObject(body.Get("output_config")).Get("format"), body.Get("output_format"))
	format := requestObject(formatRaw)
	var structured json.RawMessage
	if formatRaw != nil && string(formatRaw) != "null" && !format.IsObject() {
		return requestRecord(), requestCompatibilityFailure("Structured output must contain a JSON schema format object")
	}
	if formatRaw != nil && string(formatRaw) != "null" {
		if compat.String("structuredOutput") == "unsupported" {
			return requestRecord(), requestCompatibilityFailure("This endpoint does not support the requested structured output schema")
		}
		schema := requestObject(format.Get("schema"))
		if format.String("type") != "json_schema" || format.Get("schema") == nil || !schema.IsObject() {
			return requestRecord(), requestCompatibilityFailure("Only a JSON schema object can be converted as structured output")
		}
		name := format.Get("name")
		if !requestIsString(name) || !requestTruthy(name) {
			name = requestEncode("response")
		}
		result := requestRecord(requestField("type", "json_schema"), protocol.JSONField{Name: "name", Value: name})
		if requestIsString(format.Get("description")) {
			result.Set("description", format.Get("description"))
		}
		result.Set("schema", format.Get("schema"))
		result.Set("strict", requestEncode(false))
		structured = requestEncode(result)
	}
	sampling := compat.String("sampling") == "supported" || compat.String("sampling") != "unsupported" && string(options.Get("passSamplingParams")) == "true"
	result := requestRecord(requestField("outputBudget", budget), requestField("passSamplingParams", sampling), requestField("passReasoning", compat.String("reasoning") != "unsupported"))
	result.Set("parallelToolCalls", parallel)
	result.Set("structuredOutput", structured)
	return result, nil
}
