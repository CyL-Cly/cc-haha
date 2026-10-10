package tracecapture

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const ProtocolFrameChars = 64 * 1024

var protocolAtom = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,80}$`)

type ProtocolObserver struct {
	mu                                            sync.Mutex
	protocol, transport                           string
	bytesObserved, droppedFrames, malformedFrames int64
	termination, usage, outputBudget              protocol.JSONObject
	pendingUTF8                                   []byte
	firstDecode                                   bool
	line, data                                    strings.Builder
	event                                         string
	frameChars                                    int
	oversized, afterCR                            bool
}

func NewProtocolObserver(kind string, request json.RawMessage, budget protocol.JSONObject) (*ProtocolObserver, error) {
	if kind != "openai_chat" && kind != "openai_responses" {
		return nil, &Error{"INVALID_PROXY_TRACE_PROTOCOL", "Proxy trace protocol must be openai_chat or openai_responses"}
	}
	o := &ProtocolObserver{protocol: kind, transport: "open", termination: obj(), usage: obj(), firstDecode: true}
	wire := obj()
	fields := []string{}
	r := object(request)
	for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if safeCount(r.Get(field)) {
			wire.Set(field, r.Get(field))
			fields = append(fields, field)
		}
	}
	source := budget.Get("source")
	if source == nil || bytes.Equal(source, []byte("null")) {
		source = jstr("unknown")
	}
	field := "omit"
	if len(fields) == 1 {
		field = fields[0]
	} else if len(fields) > 1 {
		field = "multiple"
	}
	o.outputBudget = obj("source", source, "field", field)
	if len(fields) == 1 {
		o.outputBudget.Set("effective", wire.Get(fields[0]))
	}
	o.outputBudget.Set("wireFields", raw(wire))
	if budget.IsObject() {
		for _, key := range []string{"requested", "reason", "configured", "hardLimit"} {
			if budget.Get(key) != nil {
				o.outputBudget.Set(key, budget.Get(key))
			}
		}
	}
	return o, nil
}
func safeCount(v json.RawMessage) bool {
	if !numeric(v) {
		return false
	}
	n, e := strconv.ParseFloat(string(v), 64)
	return e == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 9007199254740991 && math.Trunc(n) == n
}
func atom(v json.RawMessage) string {
	if len(v) == 0 || v[0] != '"' {
		return ""
	}
	s := text(v)
	if !protocolAtom.MatchString(s) {
		return ""
	}
	return s
}
func retainedUsage(v json.RawMessage) protocol.JSONObject {
	o := object(v)
	out := obj()
	for _, key := range []string{"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if safeCount(o.Get(key)) {
			out.Set(key, o.Get(key))
		}
	}
	for _, key := range []string{"input_tokens_details", "output_tokens_details", "prompt_tokens_details", "completion_tokens_details"} {
		details := object(o.Get(key))
		retained := obj()
		for _, field := range []string{"cached_tokens", "reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"} {
			if safeCount(details.Get(field)) {
				retained.Set(field, details.Get(field))
			}
		}
		if len(retained.OwnProperties()) > 0 {
			out.Set(key, raw(retained))
		}
	}
	return out
}
func (o *ProtocolObserver) Push(chunk []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.transport != "open" {
		return
	}
	o.bytesObserved += int64(len(chunk))
	for start := 0; start < len(chunk); start += 8192 {
		part := chunk[start:min(start+8192, len(chunk))]
		stripBOM := o.firstDecode && bytes.HasPrefix(part, []byte{0xef, 0xbb, 0xbf})
		o.firstDecode = false
		buffer := append(o.pendingUTF8, part...)
		o.pendingUTF8 = nil
		end := len(buffer)
		for n := max(0, len(buffer)-3); n < len(buffer); n++ {
			if buffer[n] >= 0xc2 && buffer[n] <= 0xf4 && !utf8.FullRune(buffer[n:]) {
				end = n
				o.pendingUTF8 = append([]byte(nil), buffer[n:]...)
				break
			}
		}
		decoded := protocol.DecodeJavaScriptUTF8(buffer[:end])
		if stripBOM {
			decoded = bytes.TrimPrefix(decoded, []byte{0xef, 0xbb, 0xbf})
		}
		o.pushText(string(decoded))
	}
}
func (o *ProtocolObserver) pushText(s string) {
	for _, c := range s {
		if o.afterCR {
			o.afterCR = false
			if c == '\n' {
				continue
			}
		}
		if c == '\r' || c == '\n' {
			o.consumeLine()
			o.afterCR = c == '\r'
			continue
		}
		o.frameChars++
		if o.frameChars > ProtocolFrameChars {
			o.oversized = true
			o.line.Reset()
			o.line.WriteByte('x')
			o.data.Reset()
		} else if !o.oversized {
			o.line.WriteRune(c)
		} else {
			o.line.Reset()
			o.line.WriteByte('x')
		}
	}
}
func (o *ProtocolObserver) consumeLine() {
	line := o.line.String()
	o.line.Reset()
	if line == "" {
		if o.oversized {
			o.droppedFrames++
		} else if o.data.Len() > 0 {
			data := strings.TrimSuffix(o.data.String(), "\n")
			if text(protocol.QuoteJavaScriptUTF16(trimUnits(units(jstr(data))))) == "[DONE]" {
				o.termination.Set("doneMarker", raw(true))
			} else if json.Valid([]byte(data)) {
				o.observeJSON([]byte(data), o.event)
			} else {
				o.malformedFrames++
			}
		}
		o.data.Reset()
		o.event = ""
		o.frameChars = 0
		o.oversized = false
		return
	}
	if o.oversized || strings.HasPrefix(line, ":") {
		return
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	if field == "data" {
		o.data.WriteString(value)
		o.data.WriteByte('\n')
	} else if field == "event" {
		if protocolAtom.MatchString(value) {
			o.event = value
		} else {
			o.event = ""
		}
	}
}
func (o *ProtocolObserver) ObserveJSON(v json.RawMessage, event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observeJSON(v, event)
}
func (o *ProtocolObserver) observeJSON(v json.RawMessage, event string) {
	body := object(v)
	if !body.IsObject() {
		return
	}
	if o.protocol == "openai_chat" {
		choices := array(body.Get("choices"))
		choices = choices[:min(8, len(choices))]
		choice := protocol.JSONObject{}
		for _, v := range choices {
			c := object(v)
			if numeric(c.Get("index")) && number(c.Get("index")) == 0 {
				choice = c
				break
			}
		}
		if !choice.IsObject() && len(choices) > 0 {
			choice = object(choices[0])
		}
		if reason := atom(choice.Get("finish_reason")); reason != "" {
			o.termination.Set("finishReason", jstr(reason))
		}
		o.usage.Assign(retainedUsage(body.Get("usage")))
	} else {
		response := object(body.Get("response"))
		if !response.IsObject() {
			response = body
		}
		kind := atom(body.Get("type"))
		if kind == "" && protocolAtom.MatchString(event) {
			kind = event
		}
		switch kind {
		case "response.completed", "response.incomplete", "response.failed", "response.cancelled", "error", "response.error":
			o.termination.Set("event", jstr(kind))
		}
		if kind == "error" || kind == "response.error" {
			if code := atom(body.Get("code")); code != "" {
				o.termination.Set("errorCode", jstr(code))
			}
		}
		if status := atom(response.Get("status")); status != "" {
			o.termination.Set("responseStatus", jstr(status))
		}
		if reason := atom(object(response.Get("incomplete_details")).Get("reason")); reason != "" {
			o.termination.Set("incompleteReason", jstr(reason))
		}
		o.usage.Assign(retainedUsage(response.Get("usage")))
		o.observeError(object(response.Get("error")))
	}
	o.observeError(object(body.Get("error")))
}
func (o *ProtocolObserver) observeError(e protocol.JSONObject) {
	if typ := atom(e.Get("type")); typ != "" {
		o.termination.Set("errorType", jstr(typ))
	}
	if code := atom(e.Get("code")); code != "" {
		o.termination.Set("errorCode", jstr(code))
	}
}
func (o *ProtocolObserver) Finish(transport string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.transport != "open" {
		return
	}
	o.transport = transport
	o.line.Reset()
	o.data.Reset()
}
func (o *ProtocolObserver) Snapshot() protocol.JSONObject {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := obj("version", 1, "protocol", o.protocol, "transport", o.transport, "bytesObserved", o.bytesObserved, "droppedFrames", o.droppedFrames, "malformedFrames", o.malformedFrames, "termination", o.termination, "usage", o.usage, "outputBudget", o.outputBudget)
	return object(raw(v))
}

// ObserveReader is demand driven. Creating it performs no read; a diagnostic
// observation cannot change any read bytes, EOF, failure or Close outcome.
func (o *ProtocolObserver) ObserveReader(reader io.ReadCloser) io.ReadCloser {
	return &observedProxyReader{reader: reader, observer: o}
}

type observedProxyReader struct {
	reader   io.ReadCloser
	observer *ProtocolObserver
}

func (r *observedProxyReader) Read(p []byte) (n int, e error) {
	n, e = r.reader.Read(p)
	if n > 0 {
		safeProtocolObservation(func() { r.observer.Push(p[:n]) })
	}
	if e != nil {
		if e == io.EOF {
			safeProtocolObservation(func() { r.observer.Finish("eof") })
		} else {
			safeProtocolObservation(func() { r.observer.Finish("error") })
		}
	}
	return
}
func (r *observedProxyReader) Close() error {
	safeProtocolObservation(func() { r.observer.Finish("cancelled") })
	return r.reader.Close()
}

func safeProtocolObservation(operation func()) { defer func() { _ = recover() }(); operation() }
