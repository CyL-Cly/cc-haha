// Package providerproxytransport owns the desktop's protocol-translating HTTP
// routes. Conversion remains shared with the CLI and provider connectivity UI.
package providerproxytransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

type Options struct {
	Provider               func(context.Context, string) (*providerproxy.ProxyConfig, error)
	Settings               func(context.Context) (protocol.JSONObject, error)
	Environment            func() map[string]string
	EnvironmentWithContext func(context.Context) (map[string]string, error)
	Version                string
	Trace                  *tracecapture.Service
	TLS                    *tlsconfig.State
	HTTPClient             *http.Client
	NowMillis              func() int64
	// Private physical-clock tests shorten the original normalized timeout.
	TimeoutOverride time.Duration
}
type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.Provider == nil || o.Settings == nil || o.Environment == nil && o.EnvironmentWithContext == nil {
		return nil, errors.New("Provider proxy requires explicit saved provider, settings and environment boundaries")
	}
	if o.NowMillis == nil {
		o.NowMillis = func() int64 { return time.Now().UnixMilli() }
	}
	return &Service{o}, nil
}

var scopedPath = regexp.MustCompile(`^/proxy/providers/([^/]+)/v1/messages$`)

func encoded(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func object(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(raw, &o)
	return o
}
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: encoded(v)} }
func jsonResponse(status int, v any) *http.Response {
	body := encoded(v)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json;charset=utf-8"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}
func errorResponse(status int, kind, message string) *http.Response {
	return jsonResponse(status, record(field("type", "error"), field("error", record(field("type", kind), field("message", message)))))
}
func commonFailure(err error) *http.Response {
	if policy, ok := providerproxy.GetOpenAIPolicyError(encoded(map[string]any{"message": err.Error()})); ok {
		detail := record(field("type", "permission_error"))
		detail.Assign(policy)
		return jsonResponse(403, record(field("type", "error"), field("error", detail)))
	}
	status, kind := 502, "api_error"
	var compatibility *providerproxy.RequestCompatibilityError
	if errors.As(err, &compatibility) {
		status, kind = 400, "invalid_request_error"
	}
	var converted *providerproxy.ResponseError
	if errors.As(err, &converted) && converted.Status == 400 {
		status, kind = 400, "invalid_request_error"
	}
	return errorResponse(status, kind, err.Error())
}
func (s *Service) Handle(req *http.Request) (*http.Response, error) {
	match := scopedPath.FindStringSubmatch(req.URL.EscapedPath())
	id := ""
	if req.Method != http.MethodPost || req.URL.Path != "/proxy/v1/messages" && match == nil {
		return jsonResponse(404, record(field("error", "Not Found"), field("message", "Proxy only handles POST /proxy/v1/messages and POST /proxy/providers/:providerId/v1/messages"))), nil
	}
	if match != nil {
		var err error
		id, err = url.PathUnescape(match[1])
		if err != nil {
			return nil, err
		}
	}
	config, err := s.options.Provider(req.Context(), id)
	if err != nil {
		return nil, err
	}
	if config == nil {
		message := "No active provider configured for proxy"
		if id != "" {
			message = `Provider "` + id + `" is not configured for proxy`
		}
		return errorResponse(400, "invalid_request_error", message), nil
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil || !json.Valid(bodyBytes) {
		return errorResponse(400, "invalid_request_error", "Invalid JSON in request body"), nil
	}
	body := object(bodyBytes)
	if bytes.Equal(bytes.TrimSpace(bodyBytes), []byte("null")) {
		return nil, &providerproxy.ResponseError{Name: "TypeError", Message: "null is not an object (evaluating 'body.model')"}
	}
	if _, err := providerproxy.NormalizeProxyModel(body.Get("model")); err != nil {
		return nil, err
	}
	plan, err := providerproxy.PrepareProxyRequest(*config, body, req.Header, s.options.Version, id != "")
	if err != nil {
		return commonFailure(err), nil
	}
	settings, err := s.options.Settings(req.Context())
	if err != nil {
		return nil, err
	}
	var env map[string]string
	if s.options.EnvironmentWithContext != nil {
		env, err = s.options.EnvironmentWithContext(req.Context())
		if err != nil {
			return nil, err
		}
	} else {
		env = s.options.Environment()
	}
	timeout := time.Duration(networksettings.Normalize(encoded(settings)).TimeoutMillis) * time.Millisecond
	if s.options.TimeoutOverride > 0 {
		timeout = s.options.TimeoutOverride
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(timeout, func() { cancel(errors.New("The operation timed out.")) })
	closeLifetime := func() { timer.Stop(); cancel(context.Canceled) }
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, plan.URL, bytes.NewReader(encoded(plan.Upstream)))
	if err != nil {
		closeLifetime()
		return commonFailure(err), nil
	}
	pairs := []provider.HeaderPair{}
	names := make([]string, 0, len(plan.Headers))
	for name := range plan.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, v := range plan.Headers[name] {
			pairs = append(pairs, provider.HeaderPair{Name: name, Value: v})
		}
	}
	headers, err := provider.NewFetchHeaders(pairs)
	if err != nil {
		closeLifetime()
		return commonFailure(err), nil
	}
	request.Header = headers.HTTPHeader()
	// Bun fetch supplies this negotiation when the producer did not override it.
	if plan.Protocol != "anthropic" && request.Header.Get("Accept-Encoding") == "" {
		request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "*/*")
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "Bun/1.3.14")
	}
	if request.Header.Get("Connection") == "" {
		request.Header.Set("Connection", "keep-alive")
	}
	client, err := nativeprovidermodels.HTTPClientForNetwork(settings, env, plan.URL, s.options.HTTPClient)
	if err != nil {
		closeLifetime()
		return commonFailure(err), nil
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.DisableCompression = true
		if request.URL.Scheme == "https" && s.options.TLS != nil {
			transport.TLSClientConfig, err = s.options.TLS.VerifiedConfig(env)
			if err != nil {
				closeLifetime()
				return commonFailure(err), nil
			}
		}
	}
	client.CheckRedirect = fetchRedirect
	capture := s.beginCapture(config, plan, req, env)
	complete := func(outcome tracecapture.ProxyOutcome) {
		if capture != nil {
			capture.Complete(outcome)
		}
	}
	upstream, err := client.Do(request)
	if plan.Stream {
		timer.Stop()
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil && cause != context.Canceled {
			err = cause
		}
		complete(tracecapture.ProxyOutcome{Error: err})
		client.CloseIdleConnections()
		closeLifetime()
		if plan.Protocol == "anthropic" {
			return errorResponse(502, "api_error", err.Error()), nil
		}
		return commonFailure(err), nil
	}
	cleanup := func() { _ = upstream.Body.Close(); client.CloseIdleConnections(); closeLifetime() }
	upstream.Body = &onceBody{ReadCloser: upstream.Body}
	if plan.Protocol == "anthropic" {
		if !plan.Stream || upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
			raw, readErr := io.ReadAll(upstream.Body)
			cleanup()
			if readErr != nil {
				complete(tracecapture.ProxyOutcome{Error: readErr})
				return commonFailure(readErr), nil
			}
			complete(tracecapture.ProxyOutcome{ResponseStatus: &upstream.StatusCode, Headers: upstream.Header, UpstreamResponse: encoded(decodeTraceBytes(raw, upstream.Header.Get("Content-Encoding")))})
			return &http.Response{StatusCode: upstream.StatusCode, Header: providerproxy.StripProxyHopHeaders(upstream.Header), Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}, nil
		}
		responseHeaders := providerproxy.StripProxyHopHeaders(upstream.Header)
		responseHeaders.Set("Content-Type", "text/event-stream")
		responseHeaders.Set("Cache-Control", "no-cache")
		responseHeaders.Set("Connection", "keep-alive")
		idle := newIdleBody(upstream.Body, timeout)
		stream := newTraceBody(idle, capture, upstream.Header, upstream.Header.Get("Content-Encoding"), cleanup)
		return &http.Response{StatusCode: 200, Header: responseHeaders, Body: stream, ContentLength: -1}, nil
	}
	var fetchBody io.ReadCloser = upstream.Body
	if plan.Stream {
		fetchBody = newIdleBody(upstream.Body, timeout)
	}
	decoded, err := DecodeFetchBody(fetchBody, upstream.Header.Get("Content-Encoding"))
	if err != nil {
		cleanup()
		complete(tracecapture.ProxyOutcome{Error: err})
		return commonFailure(err), nil
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		raw, readErr := io.ReadAll(decoded)
		if readErr != nil {
			raw = nil
		}
		cleanup()
		text := string(protocol.DecodeJavaScriptUTF8(raw))
		text = strings.TrimPrefix(text, "\ufeff")
		status, result := upstreamFailure(upstream.StatusCode, text)
		complete(tracecapture.ProxyOutcome{ResponseStatus: &upstream.StatusCode, Headers: upstream.Header, UpstreamResponse: encoded(text), AnthropicResponse: encoded(result)})
		return jsonResponse(status, result), nil
	}
	if plan.Stream {
		observed := &observeBody{ReadCloser: decoded, capture: capture}
		var converted io.ReadCloser
		if plan.Protocol == "openai_chat" {
			converted, err = providerproxy.NewChatReader(req.Context(), observed, plan.Model, providerproxy.ChatReaderOptions{ChatStreamOptions: providerproxy.ChatStreamOptions{NowMillis: s.options.NowMillis}})
		} else {
			converted, err = providerproxy.NewResponsesReader(req.Context(), observed, plan.Model, providerproxy.ResponsesReaderOptions{ResponsesStreamOptions: providerproxy.ResponsesStreamOptions{NowMillis: s.options.NowMillis}})
		}
		if err != nil {
			cleanup()
			complete(tracecapture.ProxyOutcome{Error: err})
			return commonFailure(err), nil
		}
		stream := newTraceBody(converted, capture, upstream.Header, "", cleanup)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-cache"}, "Connection": {"keep-alive"}}, Body: stream, ContentLength: -1}, nil
	}
	raw, readErr := io.ReadAll(decoded)
	cleanup()
	if readErr != nil {
		complete(tracecapture.ProxyOutcome{Error: readErr})
		return commonFailure(readErr), nil
	}
	text := protocol.DecodeJavaScriptUTF8(raw)
	text = bytes.TrimPrefix(text, []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(text) {
		err = errors.New("Failed to parse JSON")
		complete(tracecapture.ProxyOutcome{Error: err})
		return commonFailure(err), nil
	}
	parsed := object(text)
	var result protocol.JSONObject
	status := 200
	if policy, ok := providerproxy.GetOpenAIPolicyError(text); ok {
		detail := record(field("type", "permission_error"))
		detail.Assign(policy)
		result = record(field("type", "error"), field("error", detail))
		status = 403
	} else if plan.Protocol == "openai_chat" {
		result, err = providerproxy.ChatToAnthropic(parsed, plan.Model, s.options.NowMillis)
	} else {
		result, err = providerproxy.ResponsesToAnthropic(parsed, plan.Model, providerproxy.ResponsesOptions{NowMillis: s.options.NowMillis})
	}
	if err != nil {
		complete(tracecapture.ProxyOutcome{Error: err})
		return commonFailure(err), nil
	}
	complete(tracecapture.ProxyOutcome{ResponseStatus: &upstream.StatusCode, Headers: upstream.Header, UpstreamResponse: text, AnthropicResponse: encoded(result)})
	return jsonResponse(status, result), nil
}
func (s *Service) beginCapture(config *providerproxy.ProxyConfig, plan providerproxy.ProxyRequest, req *http.Request, env map[string]string) *tracecapture.ProxyCapture {
	if s.options.Trace == nil {
		return nil
	}
	headerRecord := record()
	for name, values := range plan.Headers {
		value := strings.Join(values, ", ")
		if plan.Protocol == "anthropic" && strings.ToLower(name) != "content-type" && strings.ToLower(name) != "anthropic-version" && strings.ToLower(name) != "anthropic-beta" {
			value = "[redacted]"
		}
		headerRecord.Set(name, encoded(value))
	}
	input := tracecapture.ProxyInput{Binding: tracecapture.Binding{SessionID: protocol.TrimJavaScriptString(req.Header.Get("x-claude-code-session-id")), Provider: &tracecapture.ProviderInfo{ID: &config.ID, Name: config.Name, Format: config.APIFormat}}, Model: plan.Model, Protocol: plan.Protocol, AnthropicRequest: plan.Anthropic, UpstreamRequest: encoded(plan.Upstream), RequestHeaders: headerRecord, OutputBudget: plan.OutputBudget, Fetch: tracecapture.FetchInput{Context: req.Context(), Method: "POST", URL: plan.URL, Headers: plan.Headers}}
	capture, _ := s.options.Trace.BeginProxy(input, env)
	return capture
}
func upstreamFailure(status int, text string) (int, protocol.JSONObject) {
	if policy, ok := providerproxy.GetOpenAIPolicyError([]byte(text)); ok {
		detail := record(field("type", "permission_error"))
		detail.Assign(policy)
		return 403, record(field("type", "error"), field("error", detail))
	}
	units, _ := protocol.DecodeJavaScriptJSONString(encoded(text))
	if len(units) > 500 {
		units = units[:500]
	}
	prefix, _ := protocol.DecodeJavaScriptJSONString(encoded("Upstream returned HTTP " + strconv.Itoa(status) + ": "))
	message := protocol.QuoteJavaScriptUTF16(append(prefix, units...))
	return status, record(field("type", "error"), field("error", record(field("type", "api_error"), protocol.JSONField{Name: "message", Value: message})))
}
func fetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 127 {
		return errors.New("The response redirected too many times. For more information, pass `verbose: true` in the second argument to fetch()")
	}
	previous := via[len(via)-1]
	origin := func(u *url.URL) string {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		return u.Scheme + "://" + u.Hostname() + ":" + port
	}
	if origin(req.URL) != origin(previous.URL) {
		for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "WWW-Authenticate"} {
			req.Header.Del(name)
		}
	}
	return nil
}

// Serve writes real bodies and flushes every original SSE frame. A transport
// error after headers aborts the HTTP stream instead of inventing a success.
func (s *Service) Serve(writer http.ResponseWriter, req *http.Request) error {
	response, err := s.Handle(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		writer.Header()[name] = values
	}
	writer.WriteHeader(response.StatusCode)
	stream := strings.Contains(response.Header.Get("Content-Type"), "text/event-stream")
	if stream {
		if f, ok := writer.(http.Flusher); ok {
			f.Flush()
		}
	}
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err = writer.Write(buffer[:n]); err != nil {
				return err
			}
			if stream {
				if f, ok := writer.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
