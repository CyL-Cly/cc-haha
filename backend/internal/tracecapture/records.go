package tracecapture

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func callRecord(v json.RawMessage) protocol.JSONObject {
	o := object(v)
	for _, k := range []string{"id", "sessionId", "source", "startedAt"} {
		if r := o.Get(k); len(r) == 0 || r[0] != '"' {
			return protocol.JSONObject{}
		}
	}
	if !truthy(o.Get("request")) {
		return protocol.JSONObject{}
	}
	return o
}
func eventRecord(v json.RawMessage) protocol.JSONObject {
	o := object(v)
	for _, k := range []string{"id", "sessionId", "timestamp", "phase", "severity"} {
		if r := o.Get(k); len(r) == 0 || r[0] != '"' {
			return protocol.JSONObject{}
		}
	}
	return o
}
func parseEntry(line []byte) (call, event protocol.JSONObject) {
	line = bytes.TrimSpace(protocol.DecodeJavaScriptUTF8(line))
	if !json.Valid(line) {
		return
	}
	o := object(line)
	if !o.IsObject() {
		return
	}
	switch text(o.Get("type")) {
	case "event":
		event = eventRecord(o.Get("event"))
	case "call":
		call = callRecord(o.Get("record"))
	default:
		call = callRecord(line)
	}
	if call.IsObject() {
		call = attachUsage(call)
	}
	return
}
func extractUsage(v json.RawMessage) json.RawMessage {
	preview := text(v)
	if preview == "" {
		return nil
	}
	sse := false
	for _, line := range strings.Split(preview, "\n") {
		s := strings.TrimSpace(line)
		if s != "" {
			sse = strings.HasPrefix(s, "event:") || strings.HasPrefix(s, "data:")
			break
		}
	}
	if sse {
		acc := protocol.NewJSONObject()
		names := []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"}
		for _, line := range strings.Split(preview, "\n") {
			s := strings.TrimSpace(line)
			if !strings.HasPrefix(s, "data:") {
				continue
			}
			s = strings.TrimSpace(strings.TrimPrefix(s, "data:"))
			if !json.Valid([]byte(s)) {
				continue
			}
			o := object([]byte(s))
			var u protocol.JSONObject
			switch text(o.Get("type")) {
			case "message_start":
				u = object(object(o.Get("message")).Get("usage"))
			case "message_delta":
				u = object(o.Get("usage"))
			default:
				continue
			}
			for _, k := range names {
				if numeric(u.Get(k)) {
					acc.Set(k, u.Get(k))
				}
			}
		}
		if len(acc.OwnProperties()) == 0 {
			return nil
		}
		out := obj("inputTokens", number(acc.Get("input_tokens")), "outputTokens", number(acc.Get("output_tokens")))
		for _, pair := range [][2]string{{"cacheReadInputTokens", "cache_read_input_tokens"}, {"cacheCreationInputTokens", "cache_creation_input_tokens"}} {
			if acc.Get(pair[1]) != nil {
				out.Set(pair[0], acc.Get(pair[1]))
			}
		}
		return raw(out)
	}
	parsed := parseText(v)
	payload := object(parsed)
	if !payload.IsObject() {
		return nil
	}
	if a := object(payload.Get("anthropic")); a.IsObject() {
		payload = a
	}
	u := object(payload.Get("usage"))
	has := u.IsObject()
	if !has {
		u = payload
	}
	input := number(u.Get("input_tokens")) + number(u.Get("prompt_tokens"))
	output := number(u.Get("output_tokens")) + number(u.Get("completion_tokens"))
	cacheRead, cacheWrite := u.Get("cache_read_input_tokens"), u.Get("cache_creation_input_tokens")
	if !has && input == 0 && output == 0 && !numeric(cacheRead) && !numeric(cacheWrite) {
		return nil
	}
	out := obj("inputTokens", input, "outputTokens", output)
	if numeric(cacheRead) {
		out.Set("cacheReadInputTokens", cacheRead)
	}
	if numeric(cacheWrite) {
		out.Set("cacheCreationInputTokens", cacheWrite)
	}
	return raw(out)
}
func attachUsage(o protocol.JSONObject) protocol.JSONObject {
	response := object(o.Get("response"))
	body := object(response.Get("body"))
	if u := extractUsage(body.Get("preview")); u != nil {
		o = o.Clone()
		o.Set("usage", u)
	}
	return o
}
func callLocator(o protocol.JSONObject, ordinal, start, length, first int64) CallLocator {
	o = attachUsage(o)
	response := object(o.Get("response"))
	reqBody := object(object(o.Get("request")).Get("body"))
	resBody := object(response.Get("body"))
	usage := object(o.Get("usage"))
	status := text(o.Get("status"))
	if o.Get("status") == nil {
		status = "ok"
	}
	return CallLocator{ID: text(o.Get("id")), Ordinal: ordinal, FirstOrdinal: &first, ByteStart: start, ByteLength: length, StartedAt: text(o.Get("startedAt")), CompletedAt: nullableString(o.Get("completedAt")), Status: status, Source: text(o.Get("source")), Model: nullableString(o.Get("model")), DurationMs: nullableNumber(o.Get("durationMs")), Failed: status == "error" || truthy(o.Get("error")) || defaultNumber(response, "status", 200) >= 400, InputTokens: number(usage.Get("inputTokens")), OutputTokens: number(usage.Get("outputTokens")), RequestBytes: nullableNumber(reqBody.Get("bytes")), ResponseBytes: nullableNumber(resBody.Get("bytes")), ResponseStatus: nullableNumber(response.Get("status"))}
}
func eventLocator(o protocol.JSONObject, ordinal, start, length int64) EventLocator {
	return EventLocator{ID: text(o.Get("id")), Ordinal: ordinal, ByteStart: start, ByteLength: length, Timestamp: text(o.Get("timestamp")), Phase: text(o.Get("phase")), Severity: text(o.Get("severity")), CallID: nullableString(o.Get("callId")), Source: nullableString(o.Get("source")), Model: nullableString(o.Get("model")), Title: nullableString(o.Get("title")), Message: nullableString(o.Get("message"))}
}
func locatorFits(v any) bool {
	o := object(raw(v))
	for _, p := range o.OwnProperties() {
		k := text(p.NameJSON)
		if k == "title" || k == "message" {
			continue
		}
		if len(p.Value) > 0 && p.Value[0] == '"' {
			limit := 128
			if k == "id" || k == "callId" {
				limit = 512
			}
			if length(p.Value) > limit {
				return false
			}
		}
	}
	return true
}
func shellCall(id string, c CallLocator) protocol.JSONObject {
	o := obj("id", c.ID, "sessionId", id, "source", c.Source, "startedAt", c.StartedAt)
	if val(c.CompletedAt, "") != "" {
		o.Set("completedAt", raw(c.CompletedAt))
	}
	if c.DurationMs != nil {
		o.Set("durationMs", raw(c.DurationMs))
	}
	o.Set("status", jstr(c.Status))
	if val(c.Model, "") != "" {
		o.Set("model", raw(c.Model))
	}
	if c.InputTokens > 0 || c.OutputTokens > 0 {
		o.Set("usage", raw(obj("inputTokens", c.InputTokens, "outputTokens", c.OutputTokens)))
	}
	o.Set("request", raw(obj("method", "", "url", "", "headers", obj(), "body", EmptyBody(int64(val(c.RequestBytes, 0))))))
	if c.ResponseStatus != nil || c.ResponseBytes != nil {
		o.Set("response", raw(obj("status", val(c.ResponseStatus, 0), "headers", obj(), "body", EmptyBody(int64(val(c.ResponseBytes, 0))))))
	}
	return o
}
func shellEvent(id string, e EventLocator) protocol.JSONObject {
	o := obj("id", e.ID, "sessionId", id, "timestamp", e.Timestamp, "phase", e.Phase, "severity", e.Severity)
	for _, p := range []struct {
		k string
		v *string
	}{{"callId", e.CallID}, {"source", e.Source}, {"model", e.Model}, {"title", e.Title}, {"message", e.Message}} {
		if val(p.v, "") != "" {
			o.Set(p.k, raw(p.v))
		}
	}
	o.Set("metadata", raw(obj("traceDetailsOmitted", true, "recordBytes", e.ByteLength)))
	return o
}
func emptySummary() Summary { return Summary{Models: []ModelCount{}} }
func summarize(calls []protocol.JSONObject) Summary {
	s := emptySummary()
	s.APICalls = int64(len(calls))
	indexes := map[string]int{}
	for _, c := range calls {
		response := object(c.Get("response"))
		if text(c.Get("status")) == "error" || truthy(c.Get("error")) || defaultNumber(response, "status", 200) >= 400 {
			s.FailedCalls++
		}
		s.TotalDurationMs += number(c.Get("durationMs"))
		if model := text(c.Get("model")); model != "" {
			if n, ok := indexes[model]; ok {
				s.Models[n].Calls++
			} else {
				indexes[model] = len(s.Models)
				s.Models = append(s.Models, ModelCount{model, 1})
			}
		}
		u := object(c.Get("usage"))
		s.TotalInputTokens += number(u.Get("inputTokens"))
		s.TotalOutputTokens += number(u.Get("outputTokens"))
		s.UpdatedAt = nullableString(c.Get("completedAt"))
		if s.UpdatedAt == nil {
			s.UpdatedAt = nullableString(c.Get("startedAt"))
		}
	}
	return s
}
func sortedCalls(calls []CallLocator, compare func(string, string) int) {
	sort.SliceStable(calls, func(a, b int) bool {
		c := compare(calls[a].StartedAt, calls[b].StartedAt)
		if c != 0 {
			return c < 0
		}
		return val(calls[a].FirstOrdinal, calls[a].Ordinal) < val(calls[b].FirstOrdinal, calls[b].Ordinal)
	})
}
func sortedEvents(events []EventLocator, compare func(string, string) int) {
	sort.SliceStable(events, func(a, b int) bool {
		c := compare(events[a].Timestamp, events[b].Timestamp)
		if c != 0 {
			return c < 0
		}
		return events[a].Ordinal < events[b].Ordinal
	})
}
func boundedEntry(v json.RawMessage) json.RawMessage {
	bounded, reason := boundedValue(v)
	if reason == "" {
		return bounded
	}
	entry := object(v)
	metadata := obj("traceCaptureOmitted", obj("reason", reason))
	if text(entry.Get("type")) == "event" {
		e := object(entry.Get("event"))
		out := obj("id", slice(e.Get("id"), 512), "sessionId", slice(e.Get("sessionId"), 512), "timestamp", slice(e.Get("timestamp"), 128), "phase", slice(e.Get("phase"), 128), "severity", e.Get("severity"))
		if truthy(e.Get("callId")) {
			out.Set("callId", slice(e.Get("callId"), 512))
		}
		if e.Get("title") != nil {
			out.Set("title", slice(e.Get("title"), 256))
		}
		out.Set("message", jstr("[trace event details omitted: resource budget]"))
		out.Set("metadata", raw(metadata))
		return raw(obj("type", "event", "event", out))
	}
	c := entry
	if text(entry.Get("type")) == "call" {
		c = object(entry.Get("record"))
	}
	request := object(c.Get("request"))
	body := object(request.Get("body"))
	empty := EmptyBody(int64(number(body.Get("bytes"))))
	empty.Truncated = true
	empty.CaptureOmitted = reason
	out := obj("id", slice(c.Get("id"), 512), "sessionId", slice(c.Get("sessionId"), 512), "source", slice(c.Get("source"), 128), "startedAt", slice(c.Get("startedAt"), 128))
	for _, k := range []string{"completedAt", "model"} {
		if c.Get(k) != nil {
			out.Set(k, slice(c.Get(k), 128))
		}
	}
	for _, k := range []string{"status", "durationMs"} {
		if c.Get(k) != nil {
			out.Set(k, c.Get(k))
		}
	}
	out.Set("metadata", raw(metadata))
	out.Set("request", raw(obj("method", slice(request.Get("method"), 32), "url", slice(request.Get("url"), 8192), "headers", obj(), "body", empty)))
	if response := object(c.Get("response")); response.IsObject() {
		empty = EmptyBody(int64(number(object(response.Get("body")).Get("bytes"))))
		empty.Truncated = true
		empty.CaptureOmitted = reason
		out.Set("response", raw(obj("status", response.Get("status"), "headers", obj(), "body", empty)))
	}
	return raw(obj("type", "call", "record", out))
}

func validateCallShape(o protocol.JSONObject) error {
	request := object(o.Get("request"))
	body := bytes.TrimSpace(request.Get("body"))
	if len(body) == 0 {
		return errors.New("undefined is not an object (evaluating 'hydrated.request.body.bytes')")
	}
	if bytes.Equal(body, []byte("null")) {
		return errors.New("null is not an object (evaluating 'hydrated.request.body.bytes')")
	}
	response := bytes.TrimSpace(o.Get("response"))
	if len(response) > 0 && !bytes.Equal(response, []byte("null")) {
		b := bytes.TrimSpace(object(response).Get("body"))
		if len(b) == 0 {
			return errors.New("undefined is not an object (evaluating 'hydrated.response?.body.bytes')")
		}
		if bytes.Equal(b, []byte("null")) {
			return errors.New("null is not an object (evaluating 'hydrated.response?.body.bytes')")
		}
	}
	return nil
}
