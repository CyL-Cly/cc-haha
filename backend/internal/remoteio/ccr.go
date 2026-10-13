package remoteio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
)

type ccrClient struct {
	s                          *Service
	mu                         sync.Mutex
	epoch, authFailures        int
	currentState               string
	restored                   protocol.JSONObject
	initializationError        error
	initialized                chan struct{}
	events, internal, delivery *batchUploader
	state                      *workerStateUploader
	stream                     []protocol.JSONObject
	streamTimer                *time.Timer
	accumulator                *streamAccumulator
}

func newCCRClient(s *Service) *ccrClient {
	c := &ccrClient{s: s, initialized: make(chan struct{}), accumulator: newStreamAccumulator()}
	c.events = newBatchUploader(s, 100, 100000, 10*1024*1024, 30*time.Second, 500*time.Millisecond, c.sendEvents)
	c.internal = newBatchUploader(s, 100, 200, 10*1024*1024, 30*time.Second, 500*time.Millisecond, c.sendInternal)
	c.delivery = newBatchUploader(s, 64, 64, 0, 30*time.Second, 500*time.Millisecond, c.sendDelivery)
	c.state = newWorkerStateUploader(c)
	// initialize() sets this synchronously before its first await. Early SSE
	// catch-up frames must never report epoch zero while registration waits.
	c.epoch, _ = parseInteger(s.environment("CLAUDE_CODE_WORKER_EPOCH"))
	return c
}

func (c *ccrClient) initialize() {
	defer close(c.initialized)
	fail := func(reason string) {
		c.mu.Lock()
		c.initializationError = &InitError{Reason: reason}
		err := c.initializationError
		c.mu.Unlock()
		c.s.finish(0, err)
	}
	if len(c.authHeaders()) == 0 {
		fail("no_auth_headers")
		return
	}
	if _, ok := parseInteger(c.s.environment("CLAUDE_CODE_WORKER_EPOCH")); !ok {
		fail("missing_epoch")
		return
	}
	restored := make(chan protocol.JSONObject, 1)
	c.s.spawn(func() { restored <- c.getJSON(c.s.ctx, c.s.endpoints.Session+"/worker", c.authHeaders()) })
	patch := protocol.NewJSONObject(protocol.JSONField{Name: "worker_status", Value: quote("idle")}, protocol.JSONField{Name: "worker_epoch", Value: raw(c.epoch)}, protocol.JSONField{Name: "external_metadata", Value: raw(map[string]any{"pending_action": nil, "task_summary": nil})})
	if ok, _ := c.request(c.s.ctx, http.MethodPut, "/worker", patch, 10*time.Second); !ok {
		fail("worker_register_failed")
		return
	}
	c.mu.Lock()
	c.currentState = "idle"
	c.mu.Unlock()
	c.s.spawn(c.heartbeat)
	select {
	case response := <-restored:
		worker := object(response.Get("worker"))
		metadata := object(worker.Get("external_metadata"))
		c.mu.Lock()
		c.restored = metadata
		c.mu.Unlock()
	case <-c.s.ctx.Done():
		c.mu.Lock()
		c.initializationError = context.Cause(c.s.ctx)
		c.mu.Unlock()
	}
}

func (c *ccrClient) authHeaders() http.Header {
	h := c.s.headers(true)
	// getSessionIngressAuthHeaders only reports authentication properties.
	// UA/version headers are added by the actual request primitive later.
	for _, name := range []string{"anthropic-version", "User-Agent", "x-environment-runner-version"} {
		h.Del(name)
	}
	return h
}

func (c *ccrClient) request(ctx context.Context, method, path string, body protocol.JSONObject, timeout time.Duration) (bool, time.Duration) {
	if len(c.authHeaders()) == 0 {
		return false, 0
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.s.endpoints.Session+path, bytes.NewReader(raw(body)))
	if err != nil {
		return false, 0
	}
	req.Header = c.authHeaders()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.s.options.UserAgent != "" {
		req.Header.Set("User-Agent", c.s.options.UserAgent)
	}
	response, err := c.s.options.HTTP.Do(req)
	if err != nil {
		return false, 0
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	status := response.StatusCode
	if status >= 200 && status < 300 {
		c.mu.Lock()
		c.authFailures = 0
		c.mu.Unlock()
		return true, 0
	}
	if status == 409 {
		c.s.finish(0, errEpochMismatch)
		return false, 0
	}
	if status == 401 || status == 403 {
		if expiry, ok := jwtExpiry(c.s.ingressToken()); ok && expiry < c.s.options.Now().Unix() {
			c.s.finish(0, errors.New("CCRClient: session_token expired without refresh"))
			return false, 0
		}
		c.mu.Lock()
		c.authFailures++
		failures := c.authFailures
		c.mu.Unlock()
		if failures >= 10 {
			c.s.finish(0, errors.New("CCRClient: auth failures exhausted"))
			return false, 0
		}
	}
	if status == 429 {
		if seconds, ok := parseInteger(response.Header.Get("Retry-After")); ok && seconds >= 0 {
			return false, time.Duration(seconds) * time.Second
		}
	}
	return false, 0
}

func jwtExpiry(token string) (int64, bool) {
	pieces := strings.Split(token, ".")
	if len(pieces) != 3 {
		return 0, false
	}
	data, err := base64.RawURLEncoding.DecodeString(pieces[1])
	if err != nil {
		return 0, false
	}
	var value struct {
		Exp *float64 `json:"exp"`
	}
	if json.Unmarshal(data, &value) != nil || value.Exp == nil {
		return 0, false
	}
	return int64(*value.Exp), true
}

func (c *ccrClient) epochBody(key string, events []protocol.JSONObject) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "worker_epoch", Value: raw(c.epoch)}, protocol.JSONField{Name: key, Value: raw(events)})
}
func (c *ccrClient) sendEvents(ctx context.Context, events []protocol.JSONObject) error {
	if ok, after := c.request(ctx, http.MethodPost, "/worker/events", c.epochBody("events", events), 10*time.Second); !ok {
		return &retryableError{err: errors.New("client event POST failed"), after: after}
	}
	return nil
}
func (c *ccrClient) sendInternal(ctx context.Context, events []protocol.JSONObject) error {
	if ok, after := c.request(ctx, http.MethodPost, "/worker/internal-events", c.epochBody("events", events), 10*time.Second); !ok {
		return &retryableError{err: errors.New("internal event POST failed"), after: after}
	}
	return nil
}
func (c *ccrClient) sendDelivery(ctx context.Context, events []protocol.JSONObject) error {
	updates := make([]protocol.JSONObject, 0, len(events))
	for _, e := range events {
		updates = append(updates, protocol.NewJSONObject(protocol.JSONField{Name: "event_id", Value: e.Get("eventId")}, protocol.JSONField{Name: "status", Value: e.Get("status")}))
	}
	if ok, after := c.request(ctx, http.MethodPost, "/worker/events/delivery", c.epochBody("updates", updates), 10*time.Second); !ok {
		return &retryableError{err: errors.New("delivery POST failed"), after: after}
	}
	return nil
}
func (c *ccrClient) reportDelivery(eventID, status string) {
	item := protocol.NewJSONObject(protocol.JSONField{Name: "eventId", Value: quote(eventID)}, protocol.JSONField{Name: "status", Value: quote(status)})
	c.delivery.enqueueAsync([]protocol.JSONObject{item})
}

func (c *ccrClient) heartbeat() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.s.ctx.Done():
			return
		case <-ticker.C:
			parts := strings.Split(strings.TrimSuffix(c.s.endpoints.Session, "/"), "/")
			body := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quote(parts[len(parts)-1])}, protocol.JSONField{Name: "worker_epoch", Value: raw(c.epoch)})
			_, _ = c.request(c.s.ctx, http.MethodPost, "/worker/heartbeat", body, 5*time.Second)
		}
	}
}

func clientEvent(message protocol.JSONObject, ephemeral bool) protocol.JSONObject {
	message = message.Clone()
	if value := message.Get("uuid"); len(value) == 0 || value[0] != '"' {
		message.Set("uuid", quote(uuid.NewString()))
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "payload", Value: raw(message)})
	if ephemeral {
		event.Set("ephemeral", raw(true))
	}
	return event
}
func (c *ccrClient) writeEvent(message protocol.JSONObject) error {
	if message.String("type") == "stream_event" {
		c.mu.Lock()
		c.stream = append(c.stream, message)
		if c.streamTimer == nil {
			c.streamTimer = time.AfterFunc(100*time.Millisecond, func() { c.s.spawn(func() { _ = c.flushStream() }) })
		}
		c.mu.Unlock()
		return nil
	}
	if err := c.flushStream(); err != nil {
		return err
	}
	if message.String("type") == "assistant" {
		c.mu.Lock()
		c.accumulator.clear(message)
		c.mu.Unlock()
	}
	return c.events.enqueue(c.s.ctx, []protocol.JSONObject{clientEvent(message, false)})
}
func (c *ccrClient) flushStream() error {
	c.mu.Lock()
	if c.streamTimer != nil {
		c.streamTimer.Stop()
		c.streamTimer = nil
	}
	buffer := c.stream
	c.stream = nil
	payloads := c.accumulator.accumulate(buffer)
	c.mu.Unlock()
	events := make([]protocol.JSONObject, 0, len(payloads))
	for _, p := range payloads {
		events = append(events, clientEvent(p, true))
	}
	return c.events.enqueue(c.s.ctx, events)
}
func (c *ccrClient) flush(ctx context.Context) error {
	if err := c.flushStream(); err != nil {
		return err
	}
	return c.events.flush(ctx)
}
func (c *ccrClient) close() {
	c.mu.Lock()
	if c.streamTimer != nil {
		c.streamTimer.Stop()
		c.streamTimer = nil
	}
	c.stream = nil
	c.accumulator = newStreamAccumulator()
	c.mu.Unlock()
	c.events.close()
	c.internal.close()
	c.delivery.close()
	c.state.close()
}

func (s *Service) RestoredWorkerState(ctx context.Context) (protocol.JSONObject, error) {
	if s.ccr == nil {
		return protocol.JSONObject{}, nil
	}
	select {
	case <-s.ccr.initialized:
	case <-ctx.Done():
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	s.ccr.mu.Lock()
	defer s.ccr.mu.Unlock()
	return s.ccr.restored.Clone(), s.ccr.initializationError
}
func (s *Service) ReportDelivery(eventID, state string) {
	if s.ccr != nil {
		s.ccr.reportDelivery(eventID, state)
	}
}
func (s *Service) ReportState(state string, details *protocol.JSONObject) {
	if s.ccr == nil {
		return
	}
	c := s.ccr
	c.mu.Lock()
	if c.currentState == state && details == nil {
		c.mu.Unlock()
		return
	}
	c.currentState = state
	c.mu.Unlock()
	patch := protocol.NewJSONObject(protocol.JSONField{Name: "worker_status", Value: quote(state)}, protocol.JSONField{Name: "requires_action_details", Value: []byte("null")})
	if details != nil {
		selected := protocol.NewJSONObject()
		for _, name := range []string{"tool_name", "action_description", "request_id"} {
			selected.Set(name, details.Get(name))
		}
		patch.Set("requires_action_details", raw(selected))
	}
	c.state.enqueue(patch)
}
func (s *Service) ReportMetadata(metadata protocol.JSONObject) {
	if s.ccr != nil {
		s.ccr.state.enqueue(protocol.NewJSONObject(protocol.JSONField{Name: "external_metadata", Value: raw(metadata)}))
	}
}
func (s *Service) WriteInternalEvent(ctx context.Context, eventType string, payload protocol.JSONObject, isCompaction bool, agentID string) error {
	if s.ccr == nil {
		return nil
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(eventType)})
	message.Assign(payload)
	event := clientEvent(message, false)
	if isCompaction {
		event.Set("is_compaction", raw(true))
	}
	if agentID != "" {
		event.Set("agent_id", quote(agentID))
	}
	return s.ccr.internal.enqueue(ctx, []protocol.JSONObject{event})
}
func (s *Service) FlushInternalEvents(ctx context.Context) error {
	if s.ccr == nil {
		return nil
	}
	return s.ccr.internal.flush(ctx)
}
func (s *Service) InternalEventsPending() int {
	if s.ccr == nil {
		return 0
	}
	return s.ccr.internal.count()
}

func (c *ccrClient) getJSON(ctx context.Context, target string, headers http.Header) protocol.JSONObject {
	for attempt := 1; attempt <= 10 && ctx.Err() == nil; attempt++ {
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(call, http.MethodGet, target, nil)
		if err != nil {
			cancel()
			return protocol.JSONObject{}
		}
		req.Header = headers.Clone()
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", c.s.options.UserAgent)
		response, err := c.s.options.HTTP.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 && readErr == nil {
				cancel()
				return object(data)
			}
			if response.StatusCode == 409 {
				cancel()
				c.s.finish(0, errEpochMismatch)
				return protocol.JSONObject{}
			}
		}
		cancel()
		if attempt < 10 {
			delay := time.Duration(500 * time.Millisecond)
			for i := 1; i < attempt && delay < 30*time.Second; i++ {
				delay *= 2
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			delay += time.Duration(c.s.options.Random() * float64(500*time.Millisecond))
			if c.s.options.Wait(ctx, delay) != nil {
				break
			}
		}
	}
	return protocol.JSONObject{}
}

type InternalEvent struct {
	EventID       string              `json:"event_id"`
	EventType     string              `json:"event_type"`
	Payload       protocol.JSONObject `json:"payload"`
	EventMetadata protocol.JSONObject `json:"event_metadata,omitempty"`
	IsCompaction  bool                `json:"is_compaction"`
	CreatedAt     string              `json:"created_at"`
	AgentID       string              `json:"agent_id,omitempty"`
}

func (s *Service) ReadInternalEvents(ctx context.Context, subagents bool) ([]InternalEvent, error) {
	if s.ccr == nil {
		return nil, nil
	}
	headers := s.ccr.authHeaders()
	if len(headers) == 0 {
		return nil, nil
	}
	all := []InternalEvent{}
	cursor := ""
	for {
		u, _ := url.Parse(s.endpoints.Session + "/worker/internal-events")
		q := u.Query()
		if subagents {
			q.Set("subagents", "true")
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		u.RawQuery = q.Encode()
		page := s.ccr.getJSON(ctx, u.String(), headers)
		if !page.IsObject() {
			if err := s.Err(); err != nil {
				return nil, err
			}
			return nil, nil
		}
		var rows []InternalEvent
		if err := json.Unmarshal(page.Get("data"), &rows); err != nil && len(page.Get("data")) > 0 && string(page.Get("data")) != "null" {
			return nil, fmt.Errorf("CCR internal events: %w", err)
		}
		all = append(all, rows...)
		cursor = page.String("next_cursor")
		if cursor == "" {
			return all, nil
		}
	}
}
