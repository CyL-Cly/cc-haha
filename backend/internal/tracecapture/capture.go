package tracecapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const AbortCaptureGrace = 2 * time.Second

type ResponseCapture struct {
	Snapshot    BodySnapshot
	Aborted     bool
	AbortReason error
}
type readResult struct {
	bytes []byte
	err   error
}

func CaptureResponse(ctx context.Context, headers http.Header, clone io.ReadCloser, grace time.Duration) (ResponseCapture, error) {
	if clone == nil {
		return ResponseCapture{Snapshot: CreateBodySnapshot(nil, PreviewChars, false)}, nil
	}
	if grace <= 0 {
		grace = AbortCaptureGrace
	}
	defer clone.Close()
	if ctx.Err() != nil {
		return ResponseCapture{Snapshot: CreateBodySnapshot(jstr(""), PreviewChars, true), Aborted: true, AbortReason: context.Cause(ctx)}, nil
	}
	reads := make(chan readResult, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			b := make([]byte, 64*1024)
			n, e := clone.Read(b)
			select {
			case reads <- readResult{b[:n], e}:
			case <-stop:
				return
			}
			if e != nil {
				return
			}
		}
	}()
	body := []byte{}
	firstChunk, stripBOM := true, false
	truncated, aborted := false, false
	var reason error
	var abort <-chan time.Time
	var timer *time.Timer
	cancelSignal := ctx.Done()
	terminal := sseTerminal{enabled: strings.Contains(strings.ToLower(headers.Get("Content-Type")), "text/event-stream")}
	for {
		select {
		case <-cancelSignal:
			aborted = true
			reason = context.Cause(ctx)
			cancelSignal = nil
			go clone.Close()
			timer = time.NewTimer(grace)
			abort = timer.C
		case <-abort:
			goto finished
		case result := <-reads:
			if firstChunk && (len(result.bytes) > 0 || result.err == nil) {
				stripBOM = bytes.HasPrefix(result.bytes, []byte{0xef, 0xbb, 0xbf})
				firstChunk = false
			}
			remaining := StreamCaptureBytes - len(body)
			captured := result.bytes[:min(len(result.bytes), max(0, remaining))]
			body = append(body, captured...)
			budget := len(result.bytes) > remaining || len(body) >= StreamCaptureBytes
			if budget {
				truncated = true
			}
			if terminal.observe(captured) {
				goto finished
			}
			if budget {
				goto finished
			}
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					goto finished
				}
				if aborted || ctx.Err() != nil {
					aborted = true
					reason = context.Cause(ctx)
					goto finished
				}
				return ResponseCapture{}, result.err
			}
		}
	}
finished:
	if timer != nil {
		timer.Stop()
	}
	decoded := protocol.DecodeJavaScriptUTF8(body)
	if stripBOM {
		decoded = bytes.TrimPrefix(decoded, []byte{0xef, 0xbb, 0xbf})
	}
	value := jstr(string(decoded))
	if strings.Contains(headers.Get("Content-Type"), "application/json") {
		value = parseText(value)
	}
	return ResponseCapture{Snapshot: CreateBodySnapshot(value, PreviewChars, truncated || aborted), Aborted: aborted, AbortReason: reason}, nil
}

type sseTerminal struct {
	enabled bool
	buffer  []byte
	event   string
	data    []string
}

func (s *sseTerminal) observe(chunk []byte) bool {
	if !s.enabled {
		return false
	}
	s.buffer = append(s.buffer, chunk...)
	for {
		end := bytes.IndexByte(s.buffer, '\n')
		if end < 0 {
			return false
		}
		line := strings.TrimSuffix(string(s.buffer[:end]), "\r")
		s.buffer = s.buffer[end+1:]
		if line == "" {
			event := s.event
			if event == "" && len(s.data) > 0 {
				o := object([]byte(strings.Join(s.data, "\n")))
				event = text(o.Get("type"))
			}
			s.event = ""
			s.data = nil
			switch event {
			case "response.completed", "response.failed", "response.incomplete", "response.cancelled", "error":
				return true
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		if field == "event" {
			s.event = value
		}
		if field == "data" {
			s.data = append(s.data, value)
		}
	}
}

type fetchCapture struct {
	enabled      func() bool
	s            *Service
	binding      Binding
	input        FetchInput
	id           string
	started      time.Time
	model        json.RawMessage
	once         sync.Once
	lifeMu       sync.Mutex
	lifetimeStop func() bool
	finished     bool
	requestBody  json.RawMessage
	pending      BodySnapshot
}

func (s *Service) Bind(binding Binding) BeginFetch {
	return s.bind(binding, s.ShouldCapture, s.Enabled)
}
func (s *Service) BindProcess(binding Binding, environment map[string]string) BeginFetch {
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
	should := func() bool {
		if value, present := envBool(env["CC_HAHA_TRACE_API_CALLS"]); present {
			return value
		}
		return s.Settings(s.ctx).Enabled && env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	}
	return s.bind(binding, should, enabled)
}
func (s *Service) bind(binding Binding, shouldCapture, enabled func() bool) BeginFetch {
	return func(input FetchInput) FetchCapture {
		binding := binding
		if !shouldCapture() || input.Method != "POST" || len(input.PlainBody) == 0 {
			return nil
		}
		s.mu.Lock()
		if s.closed || s.closing {
			s.mu.Unlock()
			return nil
		}
		s.captures.Add(1)
		s.mu.Unlock()
		ctx := input.Context
		if ctx == nil {
			ctx = context.Background()
		}
		input.Context = ctx
		input.Headers = input.Headers.Clone()
		input.PlainBody = append([]byte(nil), input.PlainBody...)
		input.EncodingMetadata = input.EncodingMetadata.Clone()
		source := binding.Source
		if source == "" {
			source = "anthropic"
		}
		binding.Source = source
		started := s.o.Now()
		body := jstr(string(protocol.DecodeJavaScriptUTF8(input.PlainBody)))
		model := object(input.PlainBody).Get("model")
		if len(model) == 0 || model[0] != '"' {
			model = nil
		}
		pending := CreateBodySnapshot(slice(body, 4096), PreviewChars, length(body) > 4096)
		c := &fetchCapture{enabled: enabled, s: s, binding: binding, input: input, id: s.o.UUID(), started: started, model: model, requestBody: body, pending: pending}
		c.scheduleInitial()
		stop := context.AfterFunc(s.ctx, func() { c.FetchFailure(context.Canceled) })
		c.lifeMu.Lock()
		c.lifetimeStop = stop
		done := c.finished
		c.lifeMu.Unlock()
		if done {
			stop()
		}
		return c
	}
}
func (c *fetchCapture) callBase(full bool) protocol.JSONObject {
	request := obj("method", c.input.Method, "url", c.input.URL, "headers", sanitizeHeaders(c.input.Headers))
	if full {
		request.Set("body", c.requestBody)
	} else {
		request.Set("bodySnapshot", raw(c.pending))
	}
	out := obj("id", c.id, "sessionId", c.binding.SessionID, "source", c.binding.Source, "startedAt", iso(c.started), "request", request)
	if c.binding.QuerySource != "" {
		out.Set("querySource", jstr(c.binding.QuerySource))
	}
	if c.binding.Provider != nil {
		out.Set("provider", raw(c.binding.Provider))
	}
	if c.model != nil {
		out.Set("model", c.model)
	}
	return out
}
func (c *fetchCapture) eventBase() protocol.JSONObject {
	o := obj("sessionId", c.binding.SessionID, "callId", c.id, "source", c.binding.Source)
	if c.binding.Provider != nil {
		o.Set("provider", raw(c.binding.Provider))
	}
	if c.model != nil {
		o.Set("model", c.model)
	}
	return o
}
func (c *fetchCapture) metadata(phase string, fields ...any) protocol.JSONObject {
	o := c.input.EncodingMetadata.Clone()
	if !o.IsObject() {
		o = obj()
	}
	if phase != "" {
		o.Set("phase", jstr(phase))
	}
	o.Assign(obj(fields...))
	return o
}
func (c *fetchCapture) scheduleInitial() {
	call := c.callBase(false)
	call.Set("status", jstr("pending"))
	call.Set("metadata", raw(c.metadata("api_call_started")))
	event := c.eventBase()
	event.Assign(obj("timestamp", iso(c.started), "phase", "api_call_started", "severity", "info", "title", "API call started", "metadata", c.metadata("", "url", c.input.URL)))
	call, e := c.s.buildCall(call, c.enabled())
	c.s.report(e)
	event, e = c.s.buildEvent(event, c.enabled())
	c.s.report(e)
	c.s.mu.Lock()
	previous := c.s.tails[normalizeSessionID(c.binding.SessionID)]
	done := make(chan struct{})
	c.s.tails[normalizeSessionID(c.binding.SessionID)] = done
	c.s.tasks.Add(1)
	c.s.mu.Unlock()
	go func() {
		defer c.s.tasks.Done()
		if previous != nil {
			<-previous
		}
		c.writeInitial(call, event)
		close(done)
		c.s.mu.Lock()
		if c.s.tails[normalizeSessionID(c.binding.SessionID)] == done {
			delete(c.s.tails, normalizeSessionID(c.binding.SessionID))
		}
		c.s.mu.Unlock()
	}()
}
func (c *fetchCapture) writeInitial(call, event protocol.JSONObject) { // Build through the same record serializer, then append in one ordered job.
	if call.IsObject() {
		c.s.report(c.s.writeEntry(c.input.Context, c.binding.SessionID, obj("type", "call", "record", call)))
	}
	if event.IsObject() {
		c.s.report(c.s.writeEntry(c.input.Context, c.binding.SessionID, obj("type", "event", "event", event)))
	}
}
func (c *fetchCapture) HeadersResponse(status int, headers http.Header, clone io.ReadCloser) {
	accepted := false
	c.once.Do(func() {
		accepted = true
		go func() {
			defer c.finish()
			if clone == nil {
				c.captureFailure(status, headers, errors.New("Independent trace response clone is unavailable"))
				return
			}
			ctx, cancel := context.WithCancelCause(c.input.Context)
			stop := context.AfterFunc(c.s.ctx, func() { cancel(context.Canceled) })
			defer stop()
			defer cancel(nil)
			capture, e := CaptureResponse(ctx, headers, clone, AbortCaptureGrace)
			if e != nil {
				c.captureFailure(status, headers, e)
				return
			}
			completed := c.s.o.Now()
			call := c.callBase(true)
			call.Assign(obj("completedAt", iso(completed), "durationMs", float64(completed.Sub(c.started).Milliseconds()), "response", obj("status", status, "headers", sanitizeHeaders(headers), "bodySnapshot", capture.Snapshot)))
			event := c.eventBase()
			if capture.Aborted {
				failure := traceError(capture.AbortReason)
				call.Set("status", jstr("error"))
				call.Set("error", raw(failure))
				call.Set("metadata", raw(c.metadata("api_call_aborted", "aborted", true)))
				event.Assign(obj("timestamp", iso(completed), "phase", "api_call_aborted", "severity", "error", "title", "API call aborted", "message", failure.Get("message"), "metadata", c.metadata("", "status", status, "url", c.input.URL, "durationMs", float64(completed.Sub(c.started).Milliseconds()), "aborted", true)))
			} else {
				call.Set("metadata", raw(c.metadata("api_call_completed")))
				severity := "info"
				if status < 200 || status >= 300 {
					severity = "warning"
				}
				event.Assign(obj("phase", "api_call_completed", "severity", severity, "title", "API call completed", "metadata", c.metadata("", "status", status, "url", c.input.URL)))
			}
			_, e = c.recordCall(context.Background(), call)
			c.s.report(e)
			_, e = c.recordEvent(context.Background(), event)
			c.s.report(e)
		}()
	})
	if !accepted && clone != nil {
		_ = clone.Close()
	}
}
func (c *fetchCapture) FetchFailure(e error) {
	c.once.Do(func() {
		go func() {
			defer c.finish()
			completed := c.s.o.Now()
			aborted := c.input.Context.Err() != nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded)
			call := c.callBase(false)
			call.Assign(obj("status", "error", "completedAt", iso(completed), "durationMs", float64(completed.Sub(c.started).Milliseconds()), "error", traceError(e)))
			meta := c.metadata("api_call_failed")
			if aborted {
				meta.Set("aborted", raw(true))
			}
			call.Set("metadata", raw(meta))
			event := c.eventBase()
			meta = c.metadata("", "url", c.input.URL)
			if aborted {
				meta.Set("aborted", raw(true))
			}
			event.Assign(obj("timestamp", iso(completed), "phase", "api_call_failed", "severity", "error", "title", "API call failed", "message", errorText(e), "metadata", meta))
			_, e = c.recordCall(context.Background(), call)
			c.s.report(e)
			_, e = c.recordEvent(context.Background(), event)
			c.s.report(e)
		}()
	})
}
func (c *fetchCapture) captureFailure(status int, headers http.Header, e error) {
	completed := c.s.o.Now()
	event := c.eventBase()
	event.Assign(obj("timestamp", iso(completed), "phase", "response_capture_failed", "severity", "warning", "title", "Response capture failed", "message", errorText(e), "metadata", c.metadata("", "status", status, "url", c.input.URL)))
	_, failure := c.recordEvent(context.Background(), event)
	c.s.report(failure)
	call := c.callBase(true)
	call.Assign(obj("status", "error", "completedAt", iso(completed), "durationMs", float64(completed.Sub(c.started).Milliseconds()), "response", obj("status", status, "headers", sanitizeHeaders(headers)), "error", traceError(e), "metadata", c.metadata("response_capture_failed", "responseCaptureFailed", true)))
	_, failure = c.recordCall(context.Background(), call)
	c.s.report(failure)
}
func errorText(e error) string {
	if e == nil {
		return "Request aborted before the response completed (client timeout, stream idle watchdog, or user cancellation)"
	}
	return e.Error()
}
func traceError(e error) protocol.JSONObject {
	name := "Error"
	if e == nil || errors.Is(e, context.Canceled) {
		name = "AbortError"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		name = "TimeoutError"
	}
	if named, ok := e.(interface{ TraceErrorName() string }); ok {
		name = named.TraceErrorName()
	}
	out := obj("name", name, "message", errorText(e))
	if coded, ok := e.(interface{ TraceErrorCode() string }); ok {
		if code := coded.TraceErrorCode(); code != "" {
			out.Set("code", jstr(code))
		}
	}
	return out
}

func (c *fetchCapture) finish() {
	c.lifeMu.Lock()
	c.finished = true
	stop := c.lifetimeStop
	c.lifeMu.Unlock()
	if stop != nil {
		stop()
	}
	c.s.captures.Done()
}

func (c *fetchCapture) recordCall(ctx context.Context, input protocol.JSONObject) (protocol.JSONObject, error) {
	record, e := c.s.buildCall(input, c.enabled())
	if e != nil || !record.IsObject() {
		return record, e
	}
	e = c.s.appendEntry(ctx, c.binding.SessionID, obj("type", "call", "record", record))
	return record, e
}
func (c *fetchCapture) recordEvent(ctx context.Context, input protocol.JSONObject) (protocol.JSONObject, error) {
	event, e := c.s.buildEvent(input, c.enabled())
	if e != nil || !event.IsObject() {
		return event, e
	}
	e = c.s.appendEntry(ctx, c.binding.SessionID, obj("type", "event", "event", event))
	return event, e
}
