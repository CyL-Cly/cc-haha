package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"time"
)

type sdkControlResult struct {
	value json.RawMessage
	err   error
}
type sdkControlWait struct{ result chan sdkControlResult }

// RequestSDKControl is the original dedicated SDK channel's request/response
// handshake. Native controls continue using the existing Go owner producers.
// A queued outbound message is not evidence that this channel is ready.
func (d *DesktopSession) RequestSDKControl(ctx context.Context, request json.RawMessage, timeout time.Duration, canSend func() bool) (json.RawMessage, error) {
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return nil, errors.New("CLI session is not running")
	}
	started := time.Now()
	deadline := started.Add(timeout)
	state := d.sdkRuntime()
	for {
		if !time.Now().Before(deadline) {
			return nil, errors.New("Timed out waiting for CLI control channel to become ready")
		}
		state.mu.Lock()
		ready, closed := state.send != nil, state.closed
		state.mu.Unlock()
		if closed {
			return nil, errors.New("CLI session is not running")
		}
		if ready {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, context.Cause(ctx)
		case <-state.ctx.Done():
			timer.Stop()
			return nil, errors.New("CLI session is not running")
		}
	}
	id, e := sdkUUID()
	if e != nil {
		return nil, e
	}
	wait := &sdkControlWait{result: make(chan sdkControlResult, 1)}
	state.mu.Lock()
	if state.closed || state.send == nil || (canSend != nil && !canSend()) {
		state.mu.Unlock()
		return nil, errors.New("CLI session is not running")
	}
	if state.controls == nil {
		state.controls = map[string]*sdkControlWait{}
	}
	state.controls[id] = wait
	send := state.send
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		if state.controls[id] == wait {
			delete(state.controls, id)
		}
		state.mu.Unlock()
	}()
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	message := frame("control_request", field("request_id", id), protocol.JSONField{Name: "request", Value: request})
	if e := send(message); e != nil {
		return nil, errors.New("CLI session is not running")
	}
	remaining := time.Until(deadline)
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case result := <-wait.result:
		// Abort dispatch is synchronous in the original request Promise. The
		// transport's lifetime observes the same cancellation asynchronously,
		// so its stop result must not replace the caller's already-set cause.
		if e := context.Cause(ctx); e != nil {
			return nil, e
		}
		return result.value, result.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-state.ctx.Done():
		if e := context.Cause(ctx); e != nil {
			return nil, e
		}
		select {
		case result := <-wait.result:
			return result.value, result.err
		default:
			return nil, errors.New("CLI session stopped")
		}
	case <-timer.C:
		subtype := obj(request).String("subtype")
		if subtype == "" {
			subtype = "control"
		}
		return nil, fmt.Errorf("Timed out waiting for %s response", subtype)
	}
}
func (d *DesktopSession) acceptSDKControlResponse(data json.RawMessage) {
	m := obj(data)
	if m.String("type") != "control_response" {
		return
	}
	response := obj(m.Get("response"))
	id := response.String("request_id")
	state := d.sdkRuntime()
	state.mu.Lock()
	wait := state.controls[id]
	if wait != nil {
		delete(state.controls, id)
	}
	state.mu.Unlock()
	if wait == nil {
		return
	}
	result := sdkControlResult{}
	if response.String("subtype") == "error" {
		message := response.Get("error")
		text := "Control request failed"
		if sourceSDKTruthy(message) {
			if isSDKString(message) {
				_ = json.Unmarshal(message, &text)
			} else if string(message) == "true" {
				text = "true"
			} else if obj(message).IsObject() {
				text = "[object Object]"
			} else {
				text = string(message)
			}
		}
		result.err = errors.New(text)
	} else {
		result.value = response.Get("response")
		if !obj(result.value).IsObject() && (len(result.value) == 0 || result.value[0] != '[') {
			result.value = []byte(`{}`)
		}
	}
	wait.result <- result
}
