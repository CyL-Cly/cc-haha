package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

func (client *Anthropic) requestTimeout() (time.Duration, error) {
	duration := client.config.Timeout
	if millis := client.config.TimeoutMillis; millis != nil {
		if math.IsNaN(*millis) || math.IsInf(*millis, 0) || math.Trunc(*millis) != *millis {
			return 0, errors.New("timeout must be an integer")
		}
		if *millis < 0 {
			return 0, errors.New("timeout must be a positive integer")
		}
		// Preserve the pinned Bun timer's signed 32-bit interval behavior.
		if *millis == 0 || *millis > math.MaxInt32 {
			return time.Millisecond, nil
		}
		return time.Duration(*millis) * time.Millisecond, nil
	}
	return duration, nil
}

func (client *Anthropic) openPreparedResponse(ctx context.Context, prepared *MessagesRequest, sdkCallerAbort bool) (*HTTPExchangeResponse, context.CancelFunc, error) {
	if client.config.OwnerNativeIO != nil {
		if err := client.config.OwnerNativeIO.boundary.CheckOwner(); err != nil {
			return nil, nil, &ownerWaitFailure{err}
		}
	}
	if prepared == nil {
		return nil, nil, errors.New("Messages stream requires a prepared request")
	}
	if prepared.deferCCH && client.config.OpenAICodex == nil && client.config.GrokOAuth == nil {
		copy := *prepared
		copy.Body = []byte(SignClaudeCodeCCHInString(string(prepared.Body)))
		prepared = &copy
	}
	duration, err := client.requestTimeout()
	if err != nil {
		return nil, nil, err
	}
	base := client.config.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	if client.config.Vertex == nil {
		base = NormalizeAnthropicBaseURL(base)
	}
	path := prepared.Path
	if strings.HasSuffix(base, "/") && strings.HasPrefix(path, "/") {
		path = path[1:]
	}
	callerCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	opened := false
	defer func() {
		if !opened {
			cancel()
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("accept", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	if client.config.APIKey != "" || client.config.APIKeySet {
		request.Header.Set("x-api-key", strings.Trim(client.config.APIKey, " \t\r\n"))
	}
	if client.config.AuthToken != "" || client.config.AuthTokenSet {
		request.Header.Set("authorization", strings.Trim("Bearer "+client.config.AuthToken, " \t\r\n"))
	}
	for name, values := range client.config.DefaultHeaders {
		request.Header.Del(name)
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	request.Header.Set("content-type", "application/json")
	authOmitted := false
	for _, name := range prepared.RemovedHeaders {
		request.Header.Del(name)
		if strings.EqualFold(name, "x-api-key") || strings.EqualFold(name, "authorization") {
			authOmitted = true
		}
	}
	for name, values := range prepared.Headers {
		request.Header.Del(name)
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if client.config.Bedrock == nil && client.config.Vertex == nil && request.Header.Get("x-api-key") == "" && request.Header.Get("authorization") == "" && !authOmitted {
		return nil, nil, errors.New(`Could not resolve authentication method. Expected either apiKey or authToken to be set. Or for one of the "X-Api-Key" or "Authorization" headers to be explicitly omitted`)
	}
	if err := ctx.Err(); err != nil {
		if !sdkCallerAbort && errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, errors.New("Request timed out.")
		}
		return nil, nil, errors.New("Request was aborted.")
	}
	if client.config.FinalizeHeaders != nil {
		request.Header, err = client.config.FinalizeHeaders(request.URL.String(), request.Header)
		if err != nil {
			return nil, nil, err
		}
	}
	if client.config.Vertex != nil {
		if err := client.config.Vertex.adapt(ctx, request, prepared); err != nil {
			return nil, nil, err
		}
	}
	if client.config.Bedrock != nil {
		if err := client.config.Bedrock.adapt(ctx, request, prepared); err != nil {
			return nil, nil, err
		}
	}
	if client.config.GrokOAuth != nil && strings.HasSuffix(request.URL.Path, "/v1/messages") {
		response, failure := client.openGrokResponse(callerCtx, ctx, cancel, request, prepared.Body, duration, sdkCallerAbort)
		if failure != nil {
			return nil, nil, failure
		}
		opened = true
		return response, cancel, nil
	}
	if client.config.OpenAICodex != nil && strings.HasSuffix(request.URL.Path, "/v1/messages") {
		response, failure := client.openOpenAIResponse(callerCtx, ctx, cancel, request, prepared.Body, duration, sdkCallerAbort)
		if failure != nil {
			return nil, nil, failure
		}
		opened = true
		return response, cancel, nil
	}
	plainBody := prepared.Body
	if client.config.Bedrock != nil || client.config.Vertex != nil {
		// The wrapper rewrites before the original fetch/trace boundary. This
		// GetBody is the local byte copy installed by Bedrock.adapt.
		copy, failure := request.GetBody()
		if failure != nil {
			return nil, nil, failure
		}
		plainBody, failure = io.ReadAll(copy)
		_ = copy.Close()
		if failure != nil {
			return nil, nil, failure
		}
	}
	var capture tracecapture.FetchCapture
	if client.config.TraceBegin != nil {
		capture = client.config.TraceBegin(tracecapture.FetchInput{Context: callerCtx, Method: request.Method, URL: request.URL.String(), Headers: request.Header.Clone(), PlainBody: append([]byte(nil), plainBody...)})
	}
	fetchSucceeded := false
	var fetchFailure error
	defer func() {
		if capture != nil && !fetchSucceeded && fetchFailure != nil {
			capture.FetchFailure(fetchFailure)
		}
	}()
	observeResponse := func(response *HTTPExchangeResponse) {
		if capture == nil {
			return
		}
		clone := cloneFetchBody(response.Response.Body)
		if clone == nil {
			response.Response.Body, clone = cloneOrdinaryBody(response.Response.Body)
		}
		capture.HeadersResponse(response.Response.StatusCode, response.Response.Header.Clone(), clone)
	}
	if client.config.OwnerNativeIO != nil {
		response, failure := client.config.OwnerNativeIO.open(callerCtx, ctx, request, client.config, duration)
		if failure != nil {
			fetchFailure = failure
			return nil, nil, failure
		}
		fetchSucceeded = true
		observeResponse(response)
		opened = true
		return response, cancel, nil
	}
	var timedOut atomic.Bool
	var timer *time.Timer
	if duration <= 0 {
		timedOut.Store(true)
		cancel()
	} else {
		timer = time.AfterFunc(duration, func() { timedOut.Store(true); cancel() })
		defer timer.Stop()
	}
	var response *HTTPExchangeResponse
	if client.config.OwnerOpenIO != nil {
		exchange, ok := client.config.HTTPExchange.(*RawHTTP1Exchange)
		if !ok {
			err = fmt.Errorf("%w: owner direct exchange", ErrHTTPExchangeProfile)
		} else {
			response, err = client.config.OwnerOpenIO.open(request, exchange, timer)
		}
	} else {
		response, err = client.exchangeHTTP(request)
	}
	if timer != nil {
		timer.Stop() // SDK fetch timeout ends at headers, not at stream EOF.
	}
	if err != nil {
		fetchFailure = err
		var ownerFailure *ownerWaitFailure
		if errors.As(err, &ownerFailure) {
			return nil, nil, ownerFailure.cause
		}
		// SDK checks the original caller signal before its own fetch timeout.
		if errors.Is(err, ErrHTTPExchangeProfile) {
			return nil, nil, err
		}
		// Both signals may be aborted before a delayed fetch rejection arrives.
		if sdkCallerAbort && callerCtx.Err() != nil {
			return nil, nil, errors.New("Request was aborted.")
		}
		if timedOut.Load() || !sdkCallerAbort && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, errors.New("Request timed out.")
		}
		if errors.Is(ctx.Err(), context.Canceled) || sdkCallerAbort && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, errors.New("Request was aborted.")
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, nil, errors.New("Request timed out.")
		}
		return nil, nil, &SDKStreamError{Kind: "request_connection", Class: "APIConnectionError", Name: "Error", Message: "Connection error.", MessageJSON: []byte(`"Connection error."`), Cause: err}
	}
	fetchSucceeded = true
	observeResponse(response)
	opened = true
	return response, cancel, nil
}

// StreamMessage retains the aggregate profile used by Recovery and existing
// consumers. Harness history uses OpenMessagesStream so SDK dispatch cannot be
// preceded by aggregate shape validation or terminal-commit policy.
func (client *Anthropic) StreamMessage(ctx context.Context, prepared *MessagesRequest, emit func(protocol.StreamUpdate) error) (*protocol.Message, error) {
	exchange, cancel, err := client.openPreparedResponse(ctx, prepared, false)
	if err != nil {
		return nil, err
	}
	response := exchange.Response
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, statusError(response.StatusCode, raw)
	}
	if exchange.openAI != nil {
		physical, err := client.openAIBodyReader(response.Body)
		if err != nil {
			return nil, err
		}
		reader, err := exchange.openAI.reader(ctx, physical)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return protocol.DecodeAnthropicStream(ctx, reader, emit)
	}
	return protocol.DecodeAnthropicStream(ctx, response.Body, emit)
}

// OpenPreparedResponse exposes the same one-shot prepared HTTP entry used by
// OpenMessagesStream. The host owns response parsing and must close its lease;
// this performs no additional Prepare, model policy, body JSON or retry.
func (client *Anthropic) OpenPreparedResponse(ctx context.Context, prepared *MessagesRequest) (*HTTPExchangeResponse, context.CancelFunc, error) {
	exchange, cancel, err := client.openPreparedResponse(ctx, prepared, true)
	if err != nil {
		return nil, nil, sdkRequestError(err)
	}
	if exchange.openAI != nil {
		physical, failure := client.openAIBodyReader(exchange.Response.Body)
		if failure != nil {
			cancel()
			_ = exchange.Response.Body.Close()
			return nil, nil, failure
		}
		reader, failure := exchange.openAI.reader(ctx, physical)
		if failure != nil {
			cancel()
			_ = exchange.Response.Body.Close()
			return nil, nil, failure
		}
		exchange.Response.Body = reader
		exchange.openAI = nil
	}
	return exchange, cancel, nil
}
