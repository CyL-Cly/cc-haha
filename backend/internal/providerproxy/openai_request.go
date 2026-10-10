package providerproxy

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const OpenAICodexEndpoint = "https://chatgpt.com/backend-api/codex/responses"
const OpenAICodexOriginator = "codex_cli_rs"
const OpenAICodexUserAgent = "codex-cli/0.157.0"
const OpenAIOAuthDummyKey = "openai-oauth-dummy-key"

type OpenAIRequest struct {
	Endpoint      string
	Model         string
	Body          protocol.JSONObject
	Headers       protocol.JSONObject
	RequestStream bool
}

// PrepareOpenAIRequest is the original fetch remapping producer, after SDK
// body/header construction and before compression or any physical submission.
func PrepareOpenAIRequest(original protocol.JSONObject, tokens protocol.JSONObject, env map[string]string, session, agent json.RawMessage, turnState string) (OpenAIRequest, error) {
	prepared, err := PrepareOpenAIRequestBody(original, env, session)
	if err != nil {
		return OpenAIRequest{}, err
	}
	prepared.Headers, err = PrepareOpenAIRequestHeaders(tokens, session, agent, turnState)
	return prepared, err
}

// PrepareOpenAIRequestBody precedes token refresh in the original fetch. A
// rejected request never performs credential I/O or submits an upstream call.
func PrepareOpenAIRequestBody(original protocol.JSONObject, env map[string]string, session json.RawMessage) (OpenAIRequest, error) {
	model := ResolveOpenAICodexModel(original.String("model"), env)
	modelJSON := requestEncode(model)
	if protocol.TrimJavaScriptString(env["OPENAI_CODEX_MODEL"]) == "" && IsOpenAIResponsesModel(original.String("model")) {
		modelJSON = original.Get("model")
	}
	body := original.Clone()
	body.Set("model", modelJSON)
	options := requestRecord(requestField("preserveOpenAIReasoning", true))
	if key := ResolvePromptCacheKey(original, session); key != nil {
		options.Set("cacheKey", key)
	}
	options.Set("openAICodexOAuth", requestEncode(true))
	transformed, err := AnthropicToResponses(body, options)
	if err != nil {
		return OpenAIRequest{}, err
	}
	nativeEffort := requestObject(original.Get("output_config")).Get("effort")
	if !IsOpenAIReasoningEffort(nativeEffort) {
		nativeEffort = nil
	}
	reasoning := requestObject(transformed.Get("reasoning"))
	effort := ResolveOpenAIReasoningEffort(model, []json.RawMessage{nativeEffort, reasoning.Get("effort"), requestEncode(env["CC_HAHA_OPENAI_REASONING_EFFORT"])})
	if !reasoning.IsObject() {
		reasoning = requestRecord()
	}
	reasoning.Set("effort", requestEncode(effort))
	upstream := transformed.Clone()
	upstream.Set("reasoning", requestEncode(reasoning))
	upstream.Set("include", requestEncode([]string{"reasoning.encrypted_content"}))
	upstream.Set("stream", requestEncode(true))
	return OpenAIRequest{Endpoint: OpenAICodexEndpoint, Model: model, Body: upstream, RequestStream: requestTruthy(transformed.Get("stream"))}, nil
}

func PrepareOpenAIRequestHeaders(tokens protocol.JSONObject, session, agent json.RawMessage, turnState string) (protocol.JSONObject, error) {
	if !tokens.IsObject() {
		return protocol.JSONObject{}, errors.New("OpenAI OAuth token is missing or expired. Run claude auth login --openai again.")
	}
	headers := requestRecord(requestField("content-type", "application/json"), requestField("accept", "text/event-stream"), requestField("authorization", requestJoin([]json.RawMessage{requestEncode("Bearer "), requestString(tokens.Get("accessToken"))}, "")), requestField("originator", OpenAICodexOriginator), requestField("user-agent", OpenAICodexUserAgent))
	if identity, ok := ResolveOpenAIRequestIdentity(session, agent); ok {
		headers.Set("session-id", identity.Get("sessionId"))
		headers.Set("thread-id", identity.Get("threadId"))
	}
	if turnState != "" {
		headers.Set("x-codex-turn-state", requestEncode(turnState))
	}
	if account := tokens.Get("accountId"); requestTruthy(account) {
		headers.Set("chatgpt-account-id", requestString(account))
	}
	// Headers.set trims HTTP whitespace from each observed value. Full wire
	// validation remains at the existing provider FetchHeaders boundary.
	for _, field := range headers.OwnProperties() {
		units, _ := protocol.DecodeJavaScriptJSONString(field.Value)
		first, end := 0, len(units)
		whitespace := func(value uint16) bool { return value == '\t' || value == '\r' || value == '\n' || value == ' ' }
		for first < end && whitespace(units[first]) {
			first++
		}
		for end > first && whitespace(units[end-1]) {
			end--
		}
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		headers.Set(key, protocol.QuoteJavaScriptUTF16(units[first:end]))
	}
	return headers, nil
}
