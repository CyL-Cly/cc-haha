package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

const GrokOAuthDummyKey = "grok-oauth-dummy-key"

type GrokOAuthConfig struct {
	HasAuth        func(context.Context) (bool, error)
	FreshTokens    func(context.Context, bool) (*protocol.JSONObject, error)
	RuntimeCatalog func() []providerproxy.GrokModel
	Platform, Arch string
	NowMillis      func() int64
}

func (client *Anthropic) openGrokResponse(caller, sdkCtx context.Context, cancel context.CancelFunc, sdkRequest *http.Request, original []byte, delay time.Duration, sdkCallerAbort bool) (result *HTTPExchangeResponse, failure error) {
	config := client.config
	var timedOut atomic.Bool
	if config.OwnerNativeIO != nil {
		clock, err := config.OwnerNativeIO.armFetchClock(delay, cancel)
		if err != nil {
			return nil, err
		}
		config.nativeFetchClock = clock
		defer clock.clear()
	} else if delay <= 0 {
		timedOut.Store(true)
		cancel()
	} else {
		timer := time.AfterFunc(delay, func() { timedOut.Store(true); cancel() })
		defer timer.Stop()
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
	if err := body.UnmarshalJSON(original); err != nil {
		return nil, err
	}
	requested := providerproxy.ResolveGrokModelRaw(body.Get("model"))
	var model string
	_ = json.Unmarshal(requested, &model)
	session, _ := json.Marshal(sdkRequest.Header.Get("x-claude-code-session-id"))
	cache := providerproxy.ResolvePromptCacheKey(body, session)
	source := body.Clone()
	source.Set("model", requested)
	options := protocol.NewJSONObject()
	if len(cache) > 0 {
		options.Set("cacheKey", cache)
	}
	transformed, err := providerproxy.AnthropicToResponses(source, options)
	if err != nil {
		return nil, err
	}
	transformed.Set("model", requested)
	transformed.Set("stream", json.RawMessage("true"))
	var reasoning protocol.JSONObject
	_ = reasoning.UnmarshalJSON(transformed.Get("reasoning"))
	catalog := providerproxy.GrokModelCatalog()
	if config.GrokOAuth.RuntimeCatalog != nil {
		catalog = config.GrokOAuth.RuntimeCatalog()
	}
	effort := providerproxy.ResolveGrokReasoningEffort(model, reasoning.Get("effort"), catalog)
	if sourceJSONTruthyGrok(effort) {
		if !reasoning.IsObject() {
			reasoning = protocol.NewJSONObject()
		}
		reasoning.Set("effort", effort)
		transformed.Set("reasoning", mustOpenAIJSON(reasoning))
	} else {
		transformed.Delete("reasoning")
	}
	plain, err := protocol.ParseAndStringifyJavaScriptJSON(mustOpenAIJSON(transformed))
	if err != nil {
		return nil, err
	}
	if config.GrokOAuth.FreshTokens == nil {
		return nil, errors.New("Grok fresh-token consumer is missing")
	}
	tokens, err := config.GrokOAuth.FreshTokens(caller, false)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, errors.New("Grok OAuth token is missing or expired. Authorize Grok again in the desktop app.")
	}
	requestUpstream := func(tokens *protocol.JSONObject) (*HTTPExchangeResponse, error) {
		platform, arch := config.GrokOAuth.Platform, config.GrokOAuth.Arch
		if platform == "" {
			platform = runtime.GOOS
		}
		if arch == "" {
			arch = runtime.GOARCH
		}
		headers, err := providerproxy.BuildGrokIdentityHeaders(tokens.Get("accessToken"), platform, arch)
		if err != nil {
			return nil, err
		}
		headers.Set("x-grok-model-override", model)
		if len(cache) > 0 {
			var value string
			_ = json.Unmarshal(cache, &value)
			headers.Set("x-grok-conv-id", value)
		}
		pairs := []HeaderPair{}
		for key, values := range headers {
			for _, value := range values {
				pairs = append(pairs, HeaderPair{key, value})
			}
		}
		metadata, err := NewConstructorHeaders(pairs)
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(sdkCtx, "POST", providerproxy.GrokCLIBaseURL+"/responses", bytes.NewReader(plain))
		if err != nil {
			return nil, err
		}
		request.Header = metadata.HTTPHeader()
		var capture tracecapture.FetchCapture
		if config.TraceBegin != nil {
			capture = config.TraceBegin(tracecapture.FetchInput{Context: caller, Method: request.Method, URL: request.URL.String(), Headers: request.Header.Clone(), PlainBody: plain})
		}
		var upstream *HTTPExchangeResponse
		if config.OwnerNativeIO != nil {
			upstream, err = config.OwnerNativeIO.open(caller, sdkCtx, request, config, delay)
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
		return upstream, nil
	}
	upstream, err := requestUpstream(tokens)
	if err != nil {
		return nil, err
	}
	if upstream.Response.StatusCode == 401 {
		refreshed, refreshError := config.GrokOAuth.FreshTokens(caller, true)
		if refreshError != nil {
			_ = upstream.Response.Body.Close()
			return nil, refreshError
		}
		if refreshed != nil {
			_ = upstream.Response.Body.Close()
			upstream, err = requestUpstream(refreshed)
			if err != nil {
				return nil, err
			}
		}
	}
	response := upstream.Response
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		raw, readErr := client.readOpenAIBody(response.Body)
		if IsOwnerWaitFailure(readErr) {
			return nil, readErr
		}
		if readErr != nil {
			raw = nil
		}
		text := protocol.DecodeJavaScriptUTF8(raw)
		text = bytes.TrimPrefix(text, []byte{0xef, 0xbb, 0xbf})
		units := []uint16{}
		quoted, _ := protocol.ExactJavaScriptStringJSON(mustOpenAIJSON(string(text)))
		units, _ = protocol.DecodeJavaScriptJSONString(quoted)
		if len(units) > 500 {
			units = units[:500]
		}
		prefix, _ := protocol.DecodeJavaScriptJSONString(mustOpenAIJSON("Grok upstream returned HTTP " + httpStatusStringGrok(response.StatusCode) + ": "))
		message := protocol.QuoteJavaScriptUTF16(append(prefix, units...))
		inner := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: mustOpenAIJSON("api_error")}, protocol.JSONField{Name: "message", Value: message})
		return openAIJSONResponse(response.StatusCode, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: mustOpenAIJSON("error")}, protocol.JSONField{Name: "error", Value: mustOpenAIJSON(inner)}), protocol.NewJSONObject())
	}
	if response.Body == nil || response.Body == http.NoBody || response.StatusCode == 204 {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return openAIJSONResponse(502, openAIErrorBody("Grok upstream returned no stream body"), protocol.NewJSONObject())
	}
	if sourceJSONTruthyGrok(body.Get("stream")) {
		metadata, err := NewConstructorHeaders([]HeaderPair{{"content-type", "text/event-stream"}, {"cache-control", "no-cache"}, {"connection", "keep-alive"}})
		if err != nil {
			_ = response.Body.Close()
			return nil, err
		}
		upstream.Response = &http.Response{StatusCode: 200, Header: metadata.HTTPHeader(), Body: response.Body}
		upstream.OriginalHeaders = metadata
		upstream.openAI = &openAIStreamBinding{model: model, grok: true, nowMillis: config.GrokOAuth.NowMillis}
		return upstream, nil
	}
	defer response.Body.Close()
	physical, err := client.openAIBodyReader(response.Body)
	if err != nil {
		return nil, err
	}
	transformed, err = providerproxy.CollectResponses(sdkCtx, physical, model, providerproxy.ResponsesReaderOptions{ResponsesStreamOptions: providerproxy.ResponsesStreamOptions{NowMillis: config.GrokOAuth.NowMillis}})
	if err != nil {
		return nil, err
	}
	return openAIJSONResponse(200, transformed, protocol.NewJSONObject())
}
func sourceJSONTruthyGrok(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" || string(raw) == "0" || string(raw) == `""` {
		return false
	}
	return true
}
func httpStatusStringGrok(n int) string { return strconv.Itoa(n) }
