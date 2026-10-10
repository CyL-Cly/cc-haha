package sessioncollaboration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func ProjectWait(snapshot protocol.JSONObject, after float64) protocol.JSONObject {
	changed := []protocol.JSONObject{}
	for _, m := range list(snapshot.Get("messages")) {
		revision := m.Get("revision")
		if revision == nil || string(revision) == "null" {
			revision = snapshot.Get("revision")
		}
		if num(revision) > after {
			changed = append(changed, m)
		}
	}
	messages := []protocol.JSONObject{}
	remaining, clipped := 24000, false
	start := 0
	if len(changed) > 50 {
		start = len(changed) - 50
	}
	for i := len(changed) - 1; i >= start; i-- {
		m := clone(changed[i])
		m.Set("content", stringSlice(m.Get("content"), 0, 2000))
		if truthy(m.Get("error")) {
			m.Set("error", stringSlice(m.Get("error"), 0, 1000))
		}
		length := utf16JSONLength(m)
		if length > remaining {
			break
		}
		remaining -= length
		clipped = clipped || stringLength(m.Get("content")) < stringLength(changed[i].Get("content")) || !optionalStringEqual(m.Get("error"), changed[i].Get("error"))
		messages = append([]protocol.JSONObject{m}, messages...)
	}
	members := []protocol.JSONObject{}
	remaining = 24000
	source := list(snapshot.Get("members"))
	for _, original := range source {
		m := clone(original)
		if truthy(m.Get("result")) {
			m.Set("result", stringSlice(m.Get("result"), 0, 1000))
		}
		length := utf16JSONLength(m)
		if length > remaining || len(members) >= 100 {
			break
		}
		remaining -= length
		clipped = clipped || !optionalStringEqual(m.Get("result"), original.Get("result"))
		members = append(members, m)
	}
	out := clone(snapshot)
	out.Set("members", raw(members))
	out.Set("messages", raw(messages))
	out.Set("truncated", raw(clipped || len(messages) < len(changed) || len(members) < len(source)))
	out.Set("omittedMessages", raw(len(changed)-len(messages)))
	out.Set("omittedMembers", raw(len(source)-len(members)))
	return out
}
func (s *Service) Wait(ctx context.Context, after float64, sessionIDs []string, requested int, caller string) (protocol.JSONObject, error) {
	return s.WaitNumber(ctx, after, sessionIDs, float64(requested), caller)
}

func (s *Service) WaitNumber(ctx context.Context, after float64, sessionIDs []string, requested float64, caller string) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	timeout := requested
	if timeout < WaitMinMS {
		timeout = WaitMinMS
	}
	if timeout > WaitMaxMS {
		timeout = WaitMaxMS
	}
	note := func(snapshot protocol.JSONObject) protocol.JSONObject {
		if requested == timeout {
			return snapshot
		}
		r := clone(snapshot)
		r.Set("requestedTimeoutMs", raw(requested))
		r.Set("timeoutMs", raw(timeout))
		requestedJSON, _ := protocol.ExactJavaScriptStringJSON(raw(requested))
		timeoutJSON, _ := protocol.ExactJavaScriptStringJSON(raw(timeout))
		var requestedText, timeoutText string
		_ = json.Unmarshal(requestedJSON, &requestedText)
		_ = json.Unmarshal(timeoutJSON, &timeoutText)
		guidance := fmt.Sprintf("Requested timeout of %sms was clamped to %sms.", requestedText, timeoutText)
		if r.String("guidance") != "" {
			guidance += "\n\n" + r.String("guidance")
		}
		r.Set("guidance", raw(guidance))
		return r
	}
	s.mu.Lock()
	inputVersion := s.userInputs[caller]
	s.mu.Unlock()
	current, e := s.Status(ctx, sessionIDs)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	s.mu.Lock()
	members := parsed(s.store.Get("members"))
	member := parsed(members.Get(caller))
	blocked := false
	if member.IsObject() && member.String("sessionId") != member.String("rootSessionId") {
		used, queued := 0, false
		wanted := ids(sessionIDs)
		for _, f := range members.OwnProperties() {
			m := parsed(f.Value)
			if m.String("rootSessionId") == member.String("rootSessionId") && m.String("sessionId") != m.String("rootSessionId") {
				if m.String("state") == "running" || m.String("state") == "blocked" {
					used++
				}
				if m.String("state") == "queued" && (wanted == nil || wanted[m.String("sessionId")]) {
					queued = true
				}
			}
		}
		blocked = used >= 3 && queued
	}
	s.mu.Unlock()
	if blocked {
		r := ProjectWait(current, after)
		r.Set("waitReason", raw("capacity_blocked"))
		r.Set("guidance", raw("The three worker slots are occupied, including this turn. End the current turn to release its slot. Queued sessions will then start and automatically report completion or blockage; calling WaitSessions again does not release capacity."))
		return note(r), nil
	}
	if num(current.Get("revision")) > after {
		return note(ProjectWait(current, after)), nil
	}
	s.mu.Lock()
	wake := s.wake
	already := num(s.store.Get("revision")) > after || caller != "" && s.userInputs[caller] != inputVersion
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return protocol.JSONObject{}, context.Canceled
	}
	if !already {
		timer := time.NewTimer(time.Duration(timeout) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-wake:
		case <-ctx.Done():
			return protocol.JSONObject{}, context.Cause(ctx)
		}
	}
	s.mu.Lock()
	interrupted := caller != "" && s.userInputs[caller] != inputVersion
	closed = s.closed
	s.mu.Unlock()
	if interrupted {
		return protocol.JSONObject{}, &APIError{409, "WAIT_INTERRUPTED", "Waiting ended because the user supplied new input"}
	}
	if closed {
		return protocol.JSONObject{}, context.Canceled
	}
	snapshot, e := s.Status(ctx, sessionIDs)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return note(ProjectWait(snapshot, after)), nil
}
