package tracecapture

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ProxyInput is the actual conversion boundary. AnthropicRequest is the
// original harness request; UpstreamRequest is the request sent on the wire.
// OutputBudget must be the converter's resolved policy, never inferred from a
// local model default or invented from the requested token count.
type ProxyInput struct {
	Binding          Binding
	Model, Protocol  string
	AnthropicRequest protocol.JSONObject
	UpstreamRequest  json.RawMessage
	RequestHeaders   protocol.JSONObject
	OutputBudget     protocol.JSONObject
	Fetch            FetchInput
}
type ProxyOutcome struct {
	ResponseStatus                      *int
	Headers                             http.Header
	UpstreamResponse, AnthropicResponse json.RawMessage
	StreamSnapshot                      *BodySnapshot
	StreamDelivery                      string
	Error                               error
}
type ProxyCapture struct {
	capture         *fetchCapture
	input           ProxyInput
	observer        *ProtocolObserver
	once            sync.Once
	mu              sync.Mutex
	anthropicChunks [][]byte
	anthropicBytes  int64
	truncated       bool
}

func (s *Service) BeginProxy(input ProxyInput, environment map[string]string) (*ProxyCapture, error) {
	env := make(map[string]string, len(environment))
	for k, v := range environment {
		env[k] = v
	}
	enabled := func() bool {
		if value, present := envBool(env["CC_HAHA_TRACE_API_CALLS"]); present {
			return value
		}
		return s.Settings(s.ctx).Enabled
	}
	// The proxy's original recordCall/recordEvent producers use the settings
	// gate. Only the SDK fetch wrapper adds the desktop-entry-point condition.
	if !enabled() || len(trimUnits(units(jstr(input.Binding.SessionID)))) == 0 {
		return nil, nil
	}
	var observer *ProtocolObserver
	if input.Protocol != "anthropic" {
		var e error
		observer, e = NewProtocolObserver(input.Protocol, input.UpstreamRequest, input.OutputBudget)
		if e != nil {
			return nil, e
		}
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil, errors.New("Trace capture service is closed")
	}
	s.captures.Add(1)
	s.mu.Unlock()
	input.Fetch.Headers = input.Fetch.Headers.Clone()
	input.RequestHeaders = object(raw(input.RequestHeaders))
	input.AnthropicRequest = object(raw(input.AnthropicRequest))
	input.UpstreamRequest = append(json.RawMessage(nil), input.UpstreamRequest...)
	input.OutputBudget = object(raw(input.OutputBudget))
	if input.Fetch.Context == nil {
		input.Fetch.Context = context.Background()
	}
	binding := input.Binding
	binding.Source = "proxy"
	binding.QuerySource = ""
	c := &fetchCapture{s: s, binding: binding, input: input.Fetch, id: s.o.UUID(), started: s.o.Now(), enabled: enabled, model: jstr(input.Model)}
	p := &ProxyCapture{capture: c, input: input, observer: observer}
	p.started()
	stop := context.AfterFunc(s.ctx, func() { p.Complete(ProxyOutcome{Error: context.Canceled}) })
	c.lifeMu.Lock()
	c.lifetimeStop = stop
	done := c.finished
	c.lifeMu.Unlock()
	if done {
		stop()
	}
	return p, nil
}
func (p *ProxyCapture) started() {
	c := p.capture
	metadata := obj("phase", "upstream_fetch_started")
	if p.observer != nil {
		metadata.Set("protocolTrace", raw(p.observer.Snapshot()))
	}
	call := obj("id", c.id, "sessionId", c.binding.SessionID, "source", "proxy", "provider", c.binding.Provider, "model", p.input.Model, "status", "pending", "startedAt", iso(c.started), "request", obj("method", "POST", "url", c.input.URL, "headers", p.requestHeaders(), "bodySnapshot", CreateBodySnapshot(raw(obj("pending", true, "note", "proxy request body captured on call completion")), PreviewChars, false)), "metadata", metadata)
	event := obj("sessionId", c.binding.SessionID, "callId", c.id, "source", "proxy", "provider", c.binding.Provider, "model", p.input.Model, "timestamp", iso(c.started), "phase", "upstream_fetch_started", "severity", "info", "title", "Upstream fetch started", "metadata", obj("url", c.input.URL))
	call, e := c.s.buildCall(call, c.enabled())
	c.s.report(e)
	event, e = c.s.buildEvent(event, c.enabled())
	c.s.report(e)
	_, e = c.s.enqueue(normalizeSessionID(c.binding.SessionID), func() error {
		if call.IsObject() {
			if e := c.s.writeEntry(context.Background(), c.binding.SessionID, obj("type", "call", "record", call)); e != nil {
				return e
			}
		}
		if event.IsObject() {
			return c.s.writeEntry(context.Background(), c.binding.SessionID, obj("type", "event", "event", event))
		}
		return nil
	})
	c.s.report(e)
}
func (p *ProxyCapture) PushUpstream(chunk []byte) {
	if p.observer != nil {
		p.observer.Push(chunk)
	}
}
func (p *ProxyCapture) ObserveUpstreamJSON(value json.RawMessage, event string) {
	if p.observer != nil {
		p.observer.ObserveJSON(value, event)
	}
}
func (p *ProxyCapture) FinishTransport(transport string) {
	if p.observer != nil {
		p.observer.Finish(transport)
	}
}
func (p *ProxyCapture) ProtocolSummary() protocol.JSONObject {
	if p.observer != nil {
		return p.observer.Snapshot()
	}
	return obj()
}

// PushAnthropic observes converted chunks already emitted by the converter.
// Source's proxy cap preserves complete chunks below the byte cap and drops a
// crossing chunk in its entirety; it does not truncate that chunk into a prefix.
func (p *ProxyCapture) PushAnthropic(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.anthropicBytes += int64(len(chunk))
	if p.anthropicBytes <= StreamCaptureBytes {
		p.anthropicChunks = append(p.anthropicChunks, append([]byte(nil), chunk...))
	} else {
		p.truncated = true
	}
}
func (p *ProxyCapture) streamSnapshot() BodySnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	size := 0
	for _, b := range p.anthropicChunks {
		size += len(b)
	}
	joined := make([]byte, 0, size)
	for _, b := range p.anthropicChunks {
		joined = append(joined, b...)
	}
	decoded := protocol.DecodeJavaScriptUTF8(joined)
	if len(decoded) >= 3 && decoded[0] == 0xef && decoded[1] == 0xbb && decoded[2] == 0xbf {
		decoded = decoded[3:]
	}
	return CreateBodySnapshot(jstr(string(decoded)), PreviewChars, p.truncated)
}
func (p *ProxyCapture) Complete(outcome ProxyOutcome) {
	p.once.Do(func() { go func() { defer p.capture.finish(); p.recordCompletion(outcome) }() })
}
func (p *ProxyCapture) recordCompletion(outcome ProxyOutcome) {
	c := p.capture
	observer := p.observer
	if observer != nil && outcome.UpstreamResponse != nil {
		value := outcome.UpstreamResponse
		if len(value) > 0 && value[0] != '"' {
			observer.ObserveJSON(value, "")
		} else if length(value) <= 64*1024 {
			parsed := parseText(value)
			if len(parsed) > 0 && parsed[0] != '"' {
				observer.ObserveJSON(parsed, "")
			}
		}
		transport := "non_stream"
		if outcome.Error != nil || val(outcome.ResponseStatus, 200) >= 400 {
			transport = "error"
		}
		observer.Finish(transport)
	} else if observer != nil && outcome.StreamDelivery != "" {
		observer.Finish(outcome.StreamDelivery)
	} else if observer != nil && outcome.Error != nil {
		observer.Finish("error")
	}
	completed := c.s.o.Now()
	request := obj("anthropic", p.input.AnthropicRequest)
	if truthy(p.input.UpstreamRequest) {
		request.Set("upstream", p.input.UpstreamRequest)
	}
	call := obj("id", c.id, "sessionId", c.binding.SessionID, "source", "proxy", "provider", c.binding.Provider, "model", p.input.Model, "startedAt", iso(c.started), "completedAt", iso(completed), "durationMs", float64(completed.Sub(c.started).Milliseconds()), "request", obj("method", "POST", "url", c.input.URL, "headers", p.requestHeaders(), "body", request))
	if outcome.ResponseStatus != nil {
		response := obj("status", *outcome.ResponseStatus, "headers", sanitizeHeaders(outcome.Headers))
		snapshot := outcome.StreamSnapshot
		if snapshot == nil && outcome.StreamDelivery != "" {
			body := p.streamSnapshot()
			snapshot = &body
		}
		if snapshot != nil {
			response.Set("bodySnapshot", raw(snapshot))
		} else if outcome.UpstreamResponse != nil || outcome.AnthropicResponse != nil {
			body := obj()
			if outcome.UpstreamResponse != nil {
				body.Set("upstream", outcome.UpstreamResponse)
			}
			if outcome.AnthropicResponse != nil {
				body.Set("anthropic", outcome.AnthropicResponse)
			}
			response.Set("body", raw(body))
		}
		call.Set("response", raw(response))
	}
	phase, title, severity := "upstream_fetch_completed", "Upstream fetch completed", "info"
	if outcome.Error != nil {
		phase, title, severity = "upstream_fetch_failed", "Upstream fetch failed", "error"
		call.Set("error", raw(traceError(outcome.Error)))
	} else if val(outcome.ResponseStatus, 200) >= 400 {
		severity = "warning"
	}
	metadata := obj("phase", phase)
	if observer != nil {
		summary := observer.Snapshot()
		if outcome.StreamDelivery != "" {
			summary.Set("delivery", jstr(outcome.StreamDelivery))
		}
		metadata.Set("protocolTrace", raw(summary))
	}
	call.Set("metadata", raw(metadata))
	_, e := c.recordCall(context.Background(), call)
	c.s.report(e)
	meta := obj("url", c.input.URL)
	if outcome.ResponseStatus != nil {
		meta.Set("status", raw(*outcome.ResponseStatus))
	}
	event := obj("sessionId", c.binding.SessionID, "callId", c.id, "source", "proxy", "provider", c.binding.Provider, "model", p.input.Model, "timestamp", iso(completed), "phase", phase, "severity", severity, "title", title, "metadata", meta)
	if outcome.Error != nil {
		event.Set("message", jstr(outcome.Error.Error()))
	}
	_, e = c.recordEvent(context.Background(), event)
	c.s.report(e)
	p.mu.Lock()
	p.anthropicChunks = nil
	p.mu.Unlock()
}

func (p *ProxyCapture) requestHeaders() protocol.JSONObject {
	if p.input.RequestHeaders.IsObject() {
		return p.input.RequestHeaders
	}
	o := obj()
	for key, values := range p.capture.input.Headers {
		o.Set(key, jstr(strings.Join(values, ", ")))
	}
	return o
}
