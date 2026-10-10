package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

// FreshTokens is the original CLI credential consumer. It runs after body
// transformation on the same caller/owner and performs its own token refresh.
// Encode admits only immutable codec bytes through the existing owner await.
type OpenAICodexConfig struct {
	Env                map[string]string
	HasAuth            func(context.Context) (bool, error)
	IsClaudeSubscriber func(context.Context, bool) (bool, error)
	FreshTokens        func(context.Context) (protocol.JSONObject, error)
	Encode             providerproxy.OpenAIEncode
	AgentID            json.RawMessage
	TurnState          *providerproxy.OpenAICodexTurnState
	NowMillis          func() int64
}

type openAIStreamBinding struct {
	model     string
	grok      bool
	bridge    *providerproxy.OpenAITerminalAbortBridge
	nowMillis func() int64
}

func openAIHeaderRecord(record protocol.JSONObject) (*BunHeaders, error) {
	pairs := []HeaderPair{}
	for _, field := range record.OwnProperties() {
		var name, value string
		if err := json.Unmarshal(field.NameJSON, &name); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(field.Value, &value); err != nil {
			return nil, err
		}
		pairs = append(pairs, HeaderPair{name, value})
	}
	return NewConstructorHeaders(pairs)
}

func openAIJSONResponse(status int, body protocol.JSONObject, headers protocol.JSONObject) (*HTTPExchangeResponse, error) {
	metadata, err := openAIHeaderRecord(headers)
	if err != nil {
		return nil, err
	}
	if err = metadata.Set("content-type", "application/json;charset=utf-8"); err != nil {
		return nil, err
	}
	raw, err := protocol.ParseAndStringifyJavaScriptJSON(mustOpenAIJSON(body))
	if err != nil {
		return nil, err
	}
	return &HTTPExchangeResponse{Response: &http.Response{StatusCode: status, Header: metadata.HTTPHeader(), Body: io.NopCloser(bytes.NewReader(raw))}, OriginalHeaders: metadata, localBody: true}, nil
}

func mustOpenAIJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func (client *Anthropic) openOpenAIResponse(caller, sdkCtx context.Context, cancel context.CancelFunc, sdkRequest *http.Request, original []byte, delay time.Duration, sdkCallerAbort bool) (result *HTTPExchangeResponse, failure error) {
	config := client.config
	var timedOut atomic.Bool
	var timer *time.Timer
	if config.OwnerNativeIO != nil {
		clock, err := config.OwnerNativeIO.armFetchClock(delay, cancel)
		if err != nil {
			return nil, err
		}
		config.nativeFetchClock = clock
		defer clock.clear()
	} else {
		if delay <= 0 {
			timedOut.Store(true)
			cancel()
		} else {
			timer = time.AfterFunc(delay, func() { timedOut.Store(true); cancel() })
			defer timer.Stop()
		}
	}
	defer func() {
		if failure == nil || IsOwnerWaitFailure(failure) || errors.Is(failure, ErrHTTPExchangeProfile) {
			return
		}
		if sdkCallerAbort && caller.Err() != nil {
			failure = errors.New("Request was aborted.")
		} else if timedOut.Load() || config.nativeFetchClock != nil && config.nativeFetchClock.timedOut || !sdkCallerAbort && errors.Is(sdkCtx.Err(), context.DeadlineExceeded) {
			failure = errors.New("Request timed out.")
		} else if sdkCtx.Err() != nil {
			failure = errors.New("Request was aborted.")
		} else {
			var sdkFailure *SDKStreamError
			if !errors.As(failure, &sdkFailure) || sdkFailure.Kind == "json_parse" {
				failure = nativeOwnerConnectionFailure(failure)
			}
		}
	}()
	var body protocol.JSONObject
	if err := json.Unmarshal(original, &body); err != nil {
		return nil, err
	}
	session := mustOpenAIJSON(sdkRequest.Header.Get("x-claude-code-session-id"))
	prepared, err := providerproxy.PrepareOpenAIRequestBody(body, config.OpenAICodex.Env, session)
	if err != nil {
		return nil, err
	}
	if config.OpenAICodex.FreshTokens == nil {
		return nil, errors.New("OpenAI fresh-token consumer is missing")
	}
	tokens, err := config.OpenAICodex.FreshTokens(caller)
	if err != nil {
		return nil, err
	}
	state := ""
	if config.OpenAICodex.TurnState != nil {
		state = config.OpenAICodex.TurnState.Get()
	}
	headers, err := providerproxy.PrepareOpenAIRequestHeaders(tokens, session, config.OpenAICodex.AgentID, state)
	if err != nil {
		return nil, err
	}
	plain, err := protocol.ParseAndStringifyJavaScriptJSON(mustOpenAIJSON(prepared.Body))
	if err != nil {
		return nil, err
	}
	encoded, err := providerproxy.EncodeOpenAIRequestBody(sdkCtx, plain, &headers, config.OpenAICodex.Env["CC_HAHA_OPENAI_REQUEST_COMPRESSION"], config.OpenAICodex.Encode)
	if err != nil {
		return nil, err
	}
	metadata, err := openAIHeaderRecord(headers)
	if err != nil {
		return nil, err
	}
	upstreamCtx := sdkCtx
	var bridge *providerproxy.OpenAITerminalAbortBridge
	if prepared.RequestStream {
		bridge = providerproxy.NewOpenAITerminalAbortBridge(sdkCtx)
		upstreamCtx = bridge.Context()
		defer func() {
			if failure != nil {
				bridge.Dispose()
			}
		}()
	}
	request, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, prepared.Endpoint, bytes.NewReader(encoded.Wire))
	if err != nil {
		return nil, err
	}
	request.Header = metadata.HTTPHeader()
	var capture tracecapture.FetchCapture
	if config.TraceBegin != nil {
		encoding := protocol.NewJSONObject()
		if encoded.Encoded {
			encoding.Set("requestEncoding", mustOpenAIJSON(encoded.Encoding))
			encoding.Set("requestPlainBytes", mustOpenAIJSON(encoded.PlainBytes))
			encoding.Set("requestWireBytes", mustOpenAIJSON(encoded.WireBytes))
		}
		capture = config.TraceBegin(tracecapture.FetchInput{Context: caller, Method: request.Method, URL: request.URL.String(), Headers: request.Header.Clone(), PlainBody: plain, EncodingMetadata: encoding})
	}
	var upstream *HTTPExchangeResponse
	if config.OwnerNativeIO != nil {
		upstream, err = config.OwnerNativeIO.open(caller, upstreamCtx, request, config, delay)
	} else {
		upstream, err = client.exchangeHTTP(request)
	}
	if err != nil {
		if capture != nil {
			capture.FetchFailure(err)
		}
		return nil, err
	}
	if capture != nil {
		clone := cloneFetchBody(upstream.Response.Body)
		if clone == nil {
			upstream.Response.Body, clone = cloneOrdinaryBody(upstream.Response.Body)
		}
		capture.HeadersResponse(upstream.Response.StatusCode, upstream.Response.Header.Clone(), clone)
	}
	response := upstream.Response
	requestID := response.Header.Get("x-request-id")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		raw, readErr := client.readOpenAIBody(response.Body)
		if IsOwnerWaitFailure(readErr) {
			return nil, readErr
		}
		if readErr != nil {
			raw = nil
		}
		if bridge != nil {
			bridge.Dispose()
		}
		text := bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(raw), []byte("\xef\xbb\xbf"))
		status, body, header := providerproxy.OpenAIUpstreamError(response.StatusCode, mustOpenAIJSON(string(text)), requestID)
		return openAIJSONResponse(status, body, header)
	}
	if sdkCtx.Err() == nil && config.OpenAICodex.TurnState != nil {
		config.OpenAICodex.TurnState.Capture(requestIDHeader(response, "x-codex-turn-state"))
	}
	if prepared.RequestStream {
		if response.Body == nil || response.Body == http.NoBody || response.StatusCode == http.StatusNoContent {
			bridge.Dispose()
			if response.Body != nil {
				_ = response.Body.Close()
			}
			return openAIJSONResponse(502, openAIErrorBody("OpenAI upstream returned no body for stream"), protocol.NewJSONObject())
		}
		pairs := []HeaderPair{{"content-type", "text/event-stream"}, {"cache-control", "no-cache"}, {"connection", "keep-alive"}, {providerproxy.OpenAICodexStreamMarkerHeader, "1"}}
		if requestID != "" {
			pairs = append(pairs, HeaderPair{"x-request-id", requestID})
		}
		metadata, err = NewConstructorHeaders(pairs)
		if err != nil {
			_ = response.Body.Close()
			return nil, err
		}
		upstream.Response = &http.Response{StatusCode: 200, Header: metadata.HTTPHeader(), Body: response.Body}
		upstream.OriginalHeaders = metadata
		upstream.openAI = &openAIStreamBinding{model: prepared.Model, bridge: bridge, nowMillis: config.OpenAICodex.NowMillis}
		return upstream, nil
	}
	defer response.Body.Close()
	physical, err := client.openAIBodyReader(response.Body)
	if err != nil {
		return nil, err
	}
	var transformed protocol.JSONObject
	if strings.Contains(strings.ToLower(response.Header.Get("content-type")), "text/event-stream") {
		transformed, err = providerproxy.CollectResponses(sdkCtx, physical, prepared.Model, providerproxy.ResponsesReaderOptions{ResponsesStreamOptions: providerproxy.ResponsesStreamOptions{OpenAICodexOAuth: true, NowMillis: config.OpenAICodex.NowMillis}})
	} else {
		var raw []byte
		raw, err = io.ReadAll(physical)
		if err == nil {
			if len(bytes.TrimSpace(raw)) == 0 {
				err = errors.New("Unexpected end of JSON input")
			} else {
				err = json.Unmarshal(raw, &transformed)
				if err != nil {
					err = sdkJSONSyntaxError(string(raw))
				}
			}
		}
		if err == nil {
			transformed, err = providerproxy.ResponsesToAnthropic(transformed, prepared.Model, providerproxy.ResponsesOptions{PreserveOpenAIReasoning: true, NowMillis: config.OpenAICodex.NowMillis})
		}
	}
	if err != nil {
		return nil, err
	}
	return openAIJSONResponse(200, transformed, protocol.NewJSONObject())
}

func openAIErrorBody(message string) protocol.JSONObject {
	inner := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: mustOpenAIJSON("api_error")}, protocol.JSONField{Name: "message", Value: mustOpenAIJSON(message)})
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: mustOpenAIJSON("error")}, protocol.JSONField{Name: "error", Value: mustOpenAIJSON(inner)})
}
func requestIDHeader(response *http.Response, name string) string { return response.Header.Get(name) }
func (client *Anthropic) openAIBodyReader(body io.ReadCloser) (io.ReadCloser, error) {
	if client.config.OwnerBodyIO != nil {
		return client.config.OwnerBodyIO.streamBody(body)
	}
	if client.config.OwnerOpenIO != nil {
		return client.config.OwnerOpenIO.streamBody(body)
	}
	return body, nil
}
func (client *Anthropic) readOpenAIBody(body io.ReadCloser) ([]byte, error) {
	if client.config.OwnerBodyIO != nil {
		return client.config.OwnerBodyIO.errorBody(body)
	}
	if client.config.OwnerOpenIO != nil {
		return client.config.OwnerOpenIO.errorBody(body)
	}
	return io.ReadAll(body)
}
