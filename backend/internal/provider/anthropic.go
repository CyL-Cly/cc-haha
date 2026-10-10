// Package provider implements provider transports shared by the CLI and agent.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

type AnthropicConfig struct {
	APIKey, AuthToken, BaseURL string
	APIKeySet, AuthTokenSet    bool
	Timeout                    time.Duration
	// TimeoutMillis preserves the JS SDK's numeric request option. Validation
	// occurs on each Messages call, before transport, rather than at startup.
	TimeoutMillis *float64
	// DefaultHeaders are client-level overrides. Stream body headers and
	// explicit per-request header values/omissions are applied afterwards.
	DefaultHeaders http.Header
	// AuxiliaryRetryHeader distinguishes the constructor's original custom
	// retry-count header from StreamConfig's pinned SDK default of zero.
	AuxiliaryRetryHeader *string
	// FinalizeHeaders is the final fetch seam, after SDK auth validation and
	// request-level overrides. It is used for first-party IDs and host proxy auth.
	FinalizeHeaders func(requestURL string, headers http.Header) (http.Header, error)
	TraceBegin      tracecapture.BeginFetch
	HTTPClient      *http.Client
	// HTTPExchange carries original wire header rows, never a reordered map.
	HTTPExchange HTTPExchange
	// OwnerOpenIO is an explicit immutable physical Open adapter.
	OwnerOpenIO *OwnerOpenIO
	// OwnerNativeIO is the explicit finite Darwin direct-HTTP1 owner ticket adapter.
	OwnerNativeIO *OwnerNativeIO
	// OwnerBodyIO independently parks demanded response-body I/O after native headers.
	OwnerBodyIO *OwnerBodyIO
	// OpenAICodex wraps this same SDK fetch boundary and original owner. It does
	// not create another agent, retry loop or message history.
	OpenAICodex             *OpenAICodexConfig
	GrokOAuth               *GrokOAuthConfig
	Bedrock                 *BedrockConfig
	Vertex                  *VertexConfig
	nativeFetchClock        *nativeFetchClock
	modelValidationJSON     bool
	modelValidationPrepared func(*MessagesRequest)
	// StreamDiagnostic receives the pinned SDK's two JSON-parse diagnostics.
	// Hosts choose the logger; this transport never writes machine stdout.
	StreamDiagnostic func(message string, value any)
}

// Anthropic uses Messages directly. Routing normalization happens only at the
// request boundary; it must never rewrite persisted provider settings.
type Anthropic struct {
	config AnthropicConfig
	client *http.Client
}

func NewAnthropic(config AnthropicConfig) *Anthropic {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Anthropic{config: config, client: client}
}

func NormalizeAnthropicBaseURL(base string) string {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return base
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") && strings.HasSuffix(trimmed, "/v1") {
		return strings.TrimSuffix(trimmed, "/v1")
	}
	return base
}

type MessageResponse struct {
	Raw json.RawMessage
}

func (response MessageResponse) Text() (string, error) {
	raw, err := response.TextJSON()
	if err != nil {
		return "", err
	}
	var text string
	err = json.Unmarshal(raw, &text)
	return text, err
}

// TextJSON retains JavaScript UTF-16 string contents in quoted JSON. Terminal
// output uses Text(), but a subsequent model request must keep lone surrogate
// code units instead of replacing them with the displayed replacement rune.
func (response MessageResponse) TextJSON() (json.RawMessage, error) {
	var message struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(response.Raw, &message); err != nil {
		return nil, err
	}
	if message.Content == nil {
		return nil, fmt.Errorf("provider response has no content array")
	}
	var text []json.RawMessage
	for _, block := range message.Content {
		var kind string
		if json.Unmarshal(block["type"], &kind) == nil && kind == "text" {
			text = append(text, block["text"])
		}
	}
	return protocol.JoinJavaScriptValuesJSON(text, "\n")
}

// CreateMessage performs one request. Recovery disables retries; the Harness
// owns retries so it can prohibit retry after any tool has committed.
func (client *Anthropic) CreateMessage(ctx context.Context, body any) (MessageResponse, error) {
	return client.createMessageAtPath(ctx, body, "/v1/messages", nil)
}

// CountTokens uses the same native auth, headers, timeout and HTTP exchange as
// Messages. It does not invoke the conversation, tools or accounting ledger.
func (client *Anthropic) CountTokens(ctx context.Context, params protocol.JSONObject) (MessageResponse, error) {
	return client.createBetaJSON(ctx, params, "/v1/messages/count_tokens?beta=true", "token-counting-2024-11-01")
}

// CreateBetaMessage is the pinned beta Messages JSON path used by token-count
// fallback. Ordinary Harness stream creation keeps its existing constructor.
func (client *Anthropic) CreateBetaMessage(ctx context.Context, params protocol.JSONObject) (MessageResponse, error) {
	return client.createBetaJSON(ctx, params, "/v1/messages?beta=true", "")
}

func (client *Anthropic) createBetaJSON(ctx context.Context, params protocol.JSONObject, path, mandatory string) (MessageResponse, error) {
	body := params.Clone()
	betas := []json.RawMessage{}
	if raw := body.Get("betas"); len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &betas); err != nil {
			return MessageResponse{}, err
		}
	}
	if mandatory != "" {
		encoded, _ := json.Marshal(mandatory)
		betas = append(betas, encoded)
	}
	header, err := protocol.JoinJavaScriptValues(betas, ",")
	if err != nil {
		return MessageResponse{}, err
	}
	body.Delete("betas")
	copy := *client
	copy.config.DefaultHeaders = client.config.DefaultHeaders.Clone()
	if copy.config.DefaultHeaders == nil {
		copy.config.DefaultHeaders = http.Header{}
	}
	if len(betas) > 0 {
		copy.config.DefaultHeaders.Set("anthropic-beta", header)
	}
	return copy.createMessageAtPath(ctx, body, path, copy.config.DefaultHeaders)
}

func (client *Anthropic) createMessageAtPath(ctx context.Context, body any, path string, headers http.Header) (MessageResponse, error) {
	duration, err := client.requestTimeout()
	if err != nil {
		return MessageResponse{}, err
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		return MessageResponse{}, err
	}
	if headers != nil {
		// The beta SDK serializes JSON.stringify directly without a newline.
		// Leave the existing Recovery CreateMessage byte contract unchanged.
		encoded.Truncate(encoded.Len() - 1)
	}
	if client.config.modelValidationJSON && client.config.modelValidationPrepared != nil {
		client.config.modelValidationPrepared(&MessagesRequest{Path: path, Headers: headers.Clone(), Body: append([]byte(nil), encoded.Bytes()...)})
	}
	if (client.config.modelValidationJSON && client.config.OwnerNativeIO != nil || client.config.OpenAICodex != nil || client.config.GrokOAuth != nil || client.config.Bedrock != nil || client.config.Vertex != nil) && strings.HasSuffix(strings.SplitN(path, "?", 2)[0], "/v1/messages") {
		var requestHeaders http.Header
		if client.config.Bedrock != nil || client.config.Vertex != nil {
			requestHeaders = headers
		}
		exchange, cancel, err := client.openPreparedResponse(ctx, &MessagesRequest{Path: path, Headers: requestHeaders, Body: append([]byte(nil), encoded.Bytes()...)}, true)
		if err != nil {
			return MessageResponse{}, sdkRequestError(err)
		}
		defer cancel()
		defer exchange.Response.Body.Close()
		var raw []byte
		if client.config.OwnerNativeIO != nil && (client.config.modelValidationJSON || client.config.Bedrock != nil || client.config.Vertex != nil) {
			if client.config.OwnerBodyIO == nil {
				return MessageResponse{}, fmt.Errorf("%w: actual Bedrock JSON body owner", ErrHTTPExchangeProfile)
			}
			raw, err = client.config.OwnerBodyIO.ReadFetchBody(exchange.Response.Body)
		} else {
			raw, err = io.ReadAll(exchange.Response.Body)
		}
		if err != nil {
			return MessageResponse{}, err
		}
		if exchange.Response.StatusCode < 200 || exchange.Response.StatusCode >= 300 {
			return MessageResponse{}, NewSDKResponseError(&exchange.Response.StatusCode, string(raw), exchange.OriginalHeaders, true)
		}
		if client.config.modelValidationJSON {
			return finishModelValidationJSON(raw, exchange.Response.StatusCode)
		}
		if !json.Valid(raw) {
			return MessageResponse{}, errors.New("provider response is not valid JSON")
		}
		return MessageResponse{Raw: raw}, nil
	}
	base := client.config.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	base = NormalizeAnthropicBaseURL(base)
	if strings.HasSuffix(base, "/") {
		path = path[1:]
	}
	endpoint := base + path
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	var timer *time.Timer
	if duration <= 0 {
		timedOut.Store(true)
		cancel()
	} else {
		timer = time.AfterFunc(duration, func() {
			timedOut.Store(true)
			cancel()
		})
		defer timer.Stop()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &encoded)
	if err != nil {
		return MessageResponse{}, err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("accept", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	if client.config.APIKey != "" || client.config.APIKeySet {
		request.Header.Set("x-api-key", client.config.APIKey)
	}
	if client.config.AuthToken != "" || client.config.AuthTokenSet {
		request.Header.Set("authorization", "Bearer "+client.config.AuthToken)
	}
	if headers != nil {
		for name, values := range headers {
			request.Header.Del(name)
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
		if client.config.FinalizeHeaders != nil {
			request.Header, err = client.config.FinalizeHeaders(endpoint, request.Header)
			if err != nil {
				return MessageResponse{}, err
			}
		}
	}
	var capture tracecapture.FetchCapture
	if headers != nil && client.config.TraceBegin != nil {
		capture = client.config.TraceBegin(tracecapture.FetchInput{Context: ctx, Method: request.Method, URL: endpoint, Headers: request.Header.Clone(), PlainBody: append([]byte{}, encoded.Bytes()...)})
	}
	exchange, err := client.exchangeHTTP(request)
	if capture != nil {
		if err != nil {
			capture.FetchFailure(err)
		} else {
			clone := cloneFetchBody(exchange.Response.Body)
			if clone == nil {
				exchange.Response.Body, clone = cloneOrdinaryBody(exchange.Response.Body)
			}
			capture.HeadersResponse(exchange.Response.StatusCode, exchange.Response.Header.Clone(), clone)
		}
	}
	// The pinned SDK clears its fetch timer when response headers arrive.
	// Reading the response body remains cancellable by the caller, but does
	// not consume the request-option timeout after fetch has completed.
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		if errors.Is(err, ErrHTTPExchangeProfile) {
			return MessageResponse{}, err
		}
		if timedOut.Load() {
			return MessageResponse{}, errors.New("Request timed out.")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return MessageResponse{}, errors.New("Request was aborted.")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return MessageResponse{}, errors.New("Request timed out.")
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return MessageResponse{}, errors.New("Request timed out.")
		}
		return MessageResponse{}, errors.New("Connection error.")
	}
	response := exchange.Response
	defer response.Body.Close()
	var raw []byte
	if client.config.modelValidationJSON && client.config.OwnerNativeIO != nil {
		if client.config.OwnerBodyIO == nil {
			return MessageResponse{}, ErrHTTPExchangeProfile
		}
		raw, err = client.config.OwnerBodyIO.ReadFetchBody(response.Body)
	} else {
		raw, err = io.ReadAll(response.Body)
	}
	if err != nil {
		return MessageResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if headers != nil {
			return MessageResponse{}, sdkAPIError(&response.StatusCode, string(raw), response.Header, true)
		}
		return MessageResponse{}, statusError(response.StatusCode, raw)
	}
	if client.config.modelValidationJSON {
		return finishModelValidationJSON(raw, response.StatusCode)
	}
	if !json.Valid(raw) {
		return MessageResponse{}, fmt.Errorf("provider response is not valid JSON")
	}
	return MessageResponse{Raw: raw}, nil
}

// APIError.makeMessage in the pinned TS SDK tests JavaScript truthiness before
// selecting error.message, and stringifies parsed JSON rather than its source.
// Keep this separate from retries; Recovery must submit only one request.
func statusError(status int, raw []byte) error {
	message := string(raw)
	if json.Valid(raw) && jsonTruthy(raw) {
		selected := raw
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && jsonTruthy(object["message"]) {
			selected = object["message"]
			if bytes.HasPrefix(bytes.TrimSpace(selected), []byte(`"`)) {
				var text string
				_ = json.Unmarshal(selected, &text)
				message = text
				selected = nil
			}
		}
		if selected != nil {
			serialized, _ := protocol.ParseAndStringifyJavaScriptJSON(selected)
			message = string(serialized)
		}
	}
	if message == "" {
		return fmt.Errorf("%d status code (no body)", status)
	}
	return fmt.Errorf("%d %s", status, message)
}

func jsonTruthy(raw []byte) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	switch value[0] {
	case '{', '[', 't':
		return true
	case '"':
		var text string
		_ = json.Unmarshal(value, &text)
		return text != ""
	default:
		number, _ := strconv.ParseFloat(string(value), 64)
		return number != 0 && !math.IsNaN(number)
	}
}
