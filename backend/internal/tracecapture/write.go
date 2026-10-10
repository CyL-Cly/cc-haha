package tracecapture

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sanitizeHeaderValue(v json.RawMessage) protocol.JSONObject {
	v, _ = boundedValue(v)
	o := object(v)
	out := obj()
	for _, p := range o.OwnProperties() {
		value, e := protocol.JavaScriptStringJSON(p.Value)
		if e != nil {
			value = jstr("undefined")
		}
		if sensitive(text(p.NameJSON)) {
			value = jstr("[redacted]")
		} else {
			value = redactText(value)
		}
		_ = out.SetPropertyJSON(p.NameJSON, value)
	}
	return out
}
func normalizeError(v json.RawMessage) json.RawMessage {
	if !truthy(v) {
		return nil
	}
	o := object(v)
	if o.IsObject() && o.Get("name") != nil && o.Get("message") != nil {
		out := obj("name", o.Get("name"), "message", redactText(o.Get("message")))
		for _, k := range []string{"code", "stack", "cause"} {
			if truthy(o.Get(k)) {
				if k == "code" {
					out.Set(k, o.Get(k))
				} else {
					out.Set(k, redactText(o.Get(k)))
				}
			}
		}
		return raw(out)
	}
	name := "object"
	if len(v) > 0 {
		switch v[0] {
		case '"':
			name = "string"
		case 't', 'f':
			name = "boolean"
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			name = "number"
		}
	}
	message, _ := protocol.JavaScriptStringJSON(v)
	return raw(obj("name", name, "message", redactText(message)))
}
func (s *Service) buildCall(input protocol.JSONObject, enableOverride ...bool) (protocol.JSONObject, error) {
	enabled := false
	if len(enableOverride) > 0 {
		enabled = enableOverride[0]
	} else {
		enabled = s.Enabled()
	}
	if length(input.Get("sessionId")) > 512 || len(trimUnits(units(input.Get("sessionId")))) == 0 || !enabled {
		return protocol.JSONObject{}, nil
	}
	started := input.Get("startedAt")
	if started == nil || string(started) == "null" {
		started = jstr(iso(s.o.Now()))
	}
	callID := input.Get("id")
	if callID == nil || string(callID) == "null" {
		callID = jstr(s.o.UUID())
	}
	source := text(input.Get("source"))
	status := input.Get("status")
	if status == nil || string(status) == "null" {
		name := "ok"
		if truthy(input.Get("error")) {
			name = "error"
		} else if !truthy(input.Get("response")) && !truthy(input.Get("completedAt")) {
			name = "pending"
		} else if defaultNumber(object(input.Get("response")), "status", 200) >= 400 {
			name = "error"
		}
		status = jstr(name)
	}
	c := obj("id", callID, "sessionId", input.Get("sessionId"), "source", input.Get("source"))
	for _, k := range []string{"querySource", "provider", "model"} {
		if truthy(input.Get(k)) {
			c.Set(k, input.Get(k))
		}
	}
	c.Set("status", status)
	c.Set("startedAt", started)
	if truthy(input.Get("completedAt")) {
		c.Set("completedAt", input.Get("completedAt"))
	}
	if numeric(input.Get("durationMs")) {
		c.Set("durationMs", input.Get("durationMs"))
	}
	if truthy(input.Get("metadata")) {
		bounded, _ := boundedValue(input.Get("metadata"))
		c.Set("metadata", redactValue(bounded, ""))
	}
	request := object(input.Get("request"))
	method := request.Get("method")
	if method == nil || string(method) == "null" {
		method = jstr("POST")
	}
	body := CreateBodySnapshot(request.Get("body"), PreviewChars, false)
	if snapshot := request.Get("bodySnapshot"); truthy(snapshot) {
		_ = json.Unmarshal(snapshot, &body)
		body = boundedSnapshot(body)
	}
	req := obj("method", method, "url", sanitizeURL(text(request.Get("url"))), "headers", sanitizeHeaderValue(request.Get("headers")), "body", body)
	if sem := semantic(request.Get("body"), source); sem != nil {
		req.Set("semantic", sem)
	}
	c.Set("request", raw(req))
	if response := object(input.Get("response")); response.IsObject() {
		body = CreateBodySnapshot(response.Get("body"), PreviewChars, false)
		if snapshot := response.Get("bodySnapshot"); truthy(snapshot) {
			_ = json.Unmarshal(snapshot, &body)
			body = boundedSnapshot(body)
		}
		c.Set("response", raw(obj("status", response.Get("status"), "headers", sanitizeHeaderValue(response.Get("headers")), "body", body)))
	}
	if e := normalizeError(input.Get("error")); e != nil {
		c.Set("error", e)
	}
	return c, nil
}
func (s *Service) buildEvent(input protocol.JSONObject, enableOverride ...bool) (protocol.JSONObject, error) {
	enabled := false
	if len(enableOverride) > 0 {
		enabled = enableOverride[0]
	} else {
		enabled = s.Enabled()
	}
	if length(input.Get("sessionId")) > 512 || len(trimUnits(units(input.Get("sessionId")))) == 0 || !enabled {
		return protocol.JSONObject{}, nil
	}
	eventID := input.Get("id")
	if eventID == nil || string(eventID) == "null" {
		eventID = jstr(s.o.UUID())
	}
	stamp := input.Get("timestamp")
	if stamp == nil || string(stamp) == "null" {
		stamp = jstr(iso(s.o.Now()))
	}
	severity := input.Get("severity")
	if severity == nil || string(severity) == "null" {
		severity = jstr("info")
	}
	event := obj("id", eventID, "sessionId", input.Get("sessionId"), "timestamp", stamp, "phase", input.Get("phase"), "severity", severity)
	for _, k := range []string{"callId", "source", "provider", "model"} {
		if truthy(input.Get(k)) {
			event.Set(k, input.Get(k))
		}
	}
	if truthy(input.Get("title")) {
		event.Set("title", slice(input.Get("title"), 256))
	}
	if truthy(input.Get("message")) {
		event.Set("message", redactText(input.Get("message")))
	}
	if truthy(input.Get("metadata")) {
		bounded, _ := boundedValue(input.Get("metadata"))
		event.Set("metadata", redactValue(bounded, ""))
	}
	return event, nil
}

func (s *Service) RecordCall(ctx context.Context, input protocol.JSONObject) (protocol.JSONObject, error) {
	c, e := s.buildCall(input)
	if e != nil || !c.IsObject() {
		return c, e
	}
	e = s.appendEntry(ctx, text(c.Get("sessionId")), obj("type", "call", "record", c))
	return c, e
}
func (s *Service) RecordEvent(ctx context.Context, input protocol.JSONObject) (protocol.JSONObject, error) {
	c, e := s.buildEvent(input)
	if e != nil || !c.IsObject() {
		return c, e
	}
	e = s.appendEntry(ctx, text(c.Get("sessionId")), obj("type", "event", "event", c))
	return c, e
}
