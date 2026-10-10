package application

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type desktopSDKState struct {
	mu           sync.Mutex
	send         func(json.RawMessage) error
	epoch        uint64
	pending      map[string]protocol.JSONObject
	autoResolved map[string]bool
	controls     map[string]*sdkControlWait
	order        []string
	stopped      bool
	outbound     []json.RawMessage
	closed       bool
	oauthToken   string
	recovering   bool
	recoveryDone chan struct{}
	cancel       context.CancelFunc
	ctx          context.Context
	payloadMu    sync.Mutex
	payloadDone  chan struct{}
}

func (d *DesktopSession) sdkRuntime() *desktopSDKState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sdk == nil {
		ctx := d.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithCancel(ctx)
		d.sdk = &desktopSDKState{pending: map[string]protocol.JSONObject{}, autoResolved: map[string]bool{}, ctx: ctx, cancel: cancel}
	}
	return d.sdk
}
func (d *DesktopSession) BindSDKResponseTransport(send func(json.RawMessage) error) func() {
	state := d.sdkRuntime()
	state.mu.Lock()
	state.epoch++
	epoch := state.epoch
	state.send = send
	queued := state.outbound
	state.outbound = nil
	for i, data := range queued {
		if err := send(data); err != nil {
			state.outbound = append(state.outbound, queued[i:]...)
			state.send = nil
			break
		}
	}
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		if state.epoch == epoch {
			state.send = nil
		}
		state.mu.Unlock()
	}
}

// Dedicated SDK output never starts or records another ASK. Its existing CLI
// producer owns its transcript; foreign permission IDs stay separate from the
// native Controller's futures and receive responses on this exact transport.
func (d *DesktopSession) AcceptSDKOutput(ctx context.Context, data json.RawMessage) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return context.Canceled
	}
	message := obj(data)
	d.acceptSDKControlResponse(data)
	state := d.sdkRuntime()
	if message.String("type") == "control_request" && obj(message.Get("request")).String("subtype") == "can_use_tool" {
		id := message.String("request_id")
		request := obj(message.Get("request"))
		initial := request.Clone()
		if message.Get("request_id") == nil || string(message.Get("request_id"))[0] != '"' {
			return nil
		}
		request.Set("input", stripSDKAutomaticMarker(request.Get("input")))
		state.mu.Lock()
		stopped := state.stopped
		if !stopped {
			if _, ok := state.pending[id]; !ok {
				state.order = append(state.order, id)
			}
			state.pending[id] = request.Clone()
		}
		state.mu.Unlock()
		if stopped {
			return d.sendSDKPermission(id, request, obj([]byte(`{"allowed":false}`)))
		}
		d.emit(sdkInitialPermissionFrame(id, initial))
		d.trackAutoQuestion(id, request.String("tool_name"), request.String("agent_id"), request.Get("input"))
		if d.onSDK != nil {
			d.onSDK(append(json.RawMessage(nil), data...))
		}
		return nil
	}
	if message.String("type") == "control_cancel_request" || message.String("type") == "control_response" {
		topID := message.String("request_id")
		response := obj(message.Get("response"))
		nestedID := response.String("request_id")
		state.mu.Lock()
		if isSDKString(message.Get("request_id")) {
			state.removePending(topID)
			d.removeAutoQuestion(topID)
		}
		if message.String("type") == "control_response" && isSDKString(response.Get("request_id")) {
			state.removePending(nestedID)
			d.removeAutoQuestion(nestedID)
		}
		state.mu.Unlock()
		id := topID
		if message.String("type") == "control_response" && isSDKString(response.Get("request_id")) {
			id = nestedID
		}
		if (message.String("type") == "control_cancel_request" && isSDKString(message.Get("request_id"))) || (message.String("type") == "control_response" && id != "") {
			fields := []protocol.JSONField{field("requestId", id), field("permissionType", "tool")}
			behavior := obj(response.Get("response")).String("behavior")
			if message.String("type") == "control_response" && (behavior == "allow" || behavior == "deny") {
				fields = append(fields, field("allowed", behavior == "allow"))
			}
			d.emit(frame("permission_resolved", fields...))
		}
		if d.onSDK != nil {
			d.onSDK(append(json.RawMessage(nil), data...))
		}
		return nil
	}
	if message.String("type") == "system" && message.String("subtype") == "api_retry" && sdkUnauthorizedStatus(message.Get("error_status")) {
		if err := d.recoverSDKOAuth(); err != nil {
			return err
		}
	}
	if !message.IsObject() {
		if d.onSDK != nil {
			d.onSDK(data)
		}
		return nil
	}
	return d.output(data)
}

func isSDKString(data json.RawMessage) bool { return len(data) > 0 && data[0] == '"' }
func sdkInitialPermissionFrame(id string, request protocol.JSONObject) json.RawMessage {
	tool := request.Get("tool_name")
	if !sourceSDKTruthy(tool) {
		tool = raw("Unknown")
	}
	input := request.Get("input")
	if !sourceSDKTruthy(input) {
		input = raw(protocol.NewJSONObject())
	}
	fields := []protocol.JSONField{field("requestId", id), {Name: "toolName", Value: tool}, {Name: "input", Value: input}}
	if value := request.Get("tool_use_id"); isSDKString(value) {
		fields = append(fields, protocol.JSONField{Name: "toolUseId", Value: value})
	}
	if value := request.Get("description"); value != nil {
		fields = append(fields, protocol.JSONField{Name: "description", Value: value})
	}
	if display := protocol.TrimJavaScriptString(request.String("display_name")); display != "" {
		fields = append(fields, field("displayName", display))
	}
	return frame("permission_request", fields...)
}
func sourceSDKTruthy(data json.RawMessage) bool {
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		return false
	}
	if isSDKString(data) {
		var text string
		_ = json.Unmarshal(data, &text)
		return len(text) > 0
	}
	var number float64
	if json.Unmarshal(data, &number) == nil {
		return number != 0
	}
	return true
}
func stripSDKAutomaticMarker(data json.RawMessage) json.RawMessage {
	input := obj(data)
	if !input.IsObject() {
		if len(data) > 0 && data[0] == '[' {
			return append(json.RawMessage(nil), data...)
		}
		return raw(protocol.NewJSONObject())
	}
	next := input.Clone()
	metadata := obj(next.Get("metadata"))
	if metadata.IsObject() {
		metadata.Delete("autoAnswered")
		next.Set("metadata", raw(metadata))
	}
	return raw(next)
}
func sdkPermissionFrame(id string, request protocol.JSONObject) json.RawMessage {
	tool := request.String("tool_name")
	if data := request.Get("tool_name"); data == nil || len(data) == 0 || data[0] != '"' {
		tool = "Unknown"
	}
	fields := []protocol.JSONField{field("requestId", id), field("toolName", tool), {Name: "input", Value: request.Get("input")}}
	for _, pair := range [][2]string{{"agent_id", "agentId"}, {"tool_use_id", "toolUseId"}, {"description", "description"}, {"display_name", "displayName"}} {
		value := request.String(pair[0])
		trimmed := protocol.TrimJavaScriptString(value)
		if trimmed != "" {
			if pair[0] == "agent_id" || pair[0] == "display_name" {
				value = trimmed
			}
			fields = append(fields, field(pair[1], value))
		}
	}
	if data := request.Get("permission_suggestions"); len(data) > 0 && data[0] == '[' {
		fields = append(fields, protocol.JSONField{Name: "permissionSuggestions", Value: data})
	}
	return frame("permission_request", fields...)
}
func (d *DesktopSession) respondSDKPermission(message protocol.JSONObject) (bool, error) {
	state := d.sdkRuntime()
	id := message.String("requestId")
	state.mu.Lock()
	request, ok := state.pending[id]
	if state.autoResolved[id] {
		state.mu.Unlock()
		return true, nil
	}
	if ok {
		state.removePending(id)
	}
	state.mu.Unlock()
	if !ok {
		return false, nil
	}
	e := d.sendSDKPermission(id, request, message)
	if e == nil {
		d.emit(frame("permission_resolved", field("requestId", id), field("permissionType", "tool"), field("allowed", string(message.Get("allowed")) == "true")))
	}
	return true, e
}
func (d *DesktopSession) sendSDKPermission(id string, request, message protocol.JSONObject) error {
	decision := protocol.NewJSONObject()
	if string(message.Get("allowed")) == "true" {
		updated := message.Get("updatedInput")
		if len(updated) == 0 || string(updated) == "null" {
			updated = raw(protocol.NewJSONObject())
		}
		if request.String("tool_name") == "AskUserQuestion" {
			updated = stripSDKAutomaticMarker(updated)
		}
		decision.Set("behavior", raw("allow"))
		decision.Set("updatedInput", updated)
		var updates []json.RawMessage
		_ = json.Unmarshal(message.Get("permissionUpdates"), &updates)
		if len(updates) > 0 {
			decision.Set("updatedPermissions", raw(updates))
		} else if message.String("rule") == "always" {
			decision.Set("updatedPermissions", sdkSessionSuggestions(request.Get("permission_suggestions"), request.String("tool_name")))
		}
	} else {
		decision.Set("behavior", raw("deny"))
		decision.Set("message", raw(buildDesktopDenyMessage(request.String("tool_name"), message.String("denyMessage"))))
	}
	response := frame("control_response", field("response", protocol.NewJSONObject(field("subtype", "success"), field("request_id", id), protocol.JSONField{Name: "response", Value: raw(decision)})))
	return d.sendSDK(response)
}
func sdkSessionSuggestions(data json.RawMessage, tool string) json.RawMessage {
	var values []json.RawMessage
	if json.Unmarshal(data, &values) != nil || len(values) == 0 {
		return raw([]any{map[string]any{"type": "addRules", "rules": []map[string]string{{"toolName": tool}}, "behavior": "allow", "destination": "session"}})
	}
	for i, value := range values {
		entry := obj(value)
		if len(value) > 0 && value[0] == '[' {
			var array []json.RawMessage
			_ = json.Unmarshal(value, &array)
			entry = protocol.NewJSONObject()
			for j, item := range array {
				entry.Set(strconv.Itoa(j), item)
			}
		}
		if entry.IsObject() {
			entry.Set("destination", raw("session"))
			values[i] = raw(entry)
		}
	}
	return raw(values)
}
func (d *DesktopSession) sdkPermissionSnapshot() []json.RawMessage {
	state := d.sdkRuntime()
	state.mu.Lock()
	defer state.mu.Unlock()
	out := []json.RawMessage{}
	for _, id := range state.order {
		if request, ok := state.pending[id]; ok {
			replay := obj(sdkPermissionFrame(id, request))
			replay.Delete("agentId")
			replay.Delete("permissionSuggestions")
			out = append(out, raw(replay))
		}
	}
	return out
}
func (d *DesktopSession) stopSDKTransport(stopped bool) error {
	state := d.sdkRuntime()
	state.mu.Lock()
	state.stopped = stopped
	state.mu.Unlock()
	if stopped {
		id, e := sdkUUID()
		if e != nil {
			return e
		}
		return d.sendSDK(frame("control_request", field("request_id", id), field("request", map[string]string{"subtype": "interrupt"})))
	}
	return nil
}

func sdkUUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (state *desktopSDKState) removePending(id string) {
	delete(state.pending, id)
	for i, v := range state.order {
		if v == id {
			state.order = append(state.order[:i], state.order[i+1:]...)
			break
		}
	}
}
func (d *DesktopSession) sendSDK(data json.RawMessage) error {
	state := d.sdkRuntime()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return context.Canceled
	}
	if state.send == nil {
		state.outbound = append(state.outbound, append(json.RawMessage(nil), data...))
		return nil
	}
	return state.send(data)
}

func (d *DesktopSession) AcceptsSDKPermissionRequests() bool {
	state := d.sdkRuntime()
	state.mu.Lock()
	defer state.mu.Unlock()
	return !state.stopped && !state.closed
}

// The original SDK handler processes all lines in one event-loop callback;
// OAuth promise continuations settle after that callback. Preserve that batch
// boundary without borrowing or opening a native ASK/Owner frame.
func (d *DesktopSession) BeginSDKPayload() func() {
	state := d.sdkRuntime()
	state.payloadMu.Lock()
	state.mu.Lock()
	done := make(chan struct{})
	state.payloadDone = done
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		state.payloadDone = nil
		close(done)
		state.mu.Unlock()
		state.payloadMu.Unlock()
	}
}

func sdkUnauthorizedStatus(data json.RawMessage) bool {
	var number float64
	if json.Unmarshal(data, &number) == nil {
		return number == 401
	}
	var text string
	if json.Unmarshal(data, &text) != nil {
		return false
	}
	number, err := strconv.ParseFloat(protocol.TrimJavaScriptString(text), 64)
	return err == nil && number == 401
}
