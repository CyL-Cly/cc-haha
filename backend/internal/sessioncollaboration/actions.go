package sessioncollaboration

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) describeCreation(ctx context.Context, result protocol.JSONObject) (protocol.JSONObject, error) {
	r, e := s.Status(ctx, []string{result.String("sessionId")})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	out := clone(result)
	state, delivery := "idle", "queued"
	for _, m := range list(r.Get("members")) {
		if m.String("sessionId") == result.String("sessionId") {
			state = m.String("state")
			break
		}
	}
	for _, m := range list(r.Get("messages")) {
		if m.String("id") == result.String("messageId") {
			delivery = m.String("status")
			if m.String("error") != "" {
				copyRaw(&out, "error", m.Get("error"))
			}
			if m.String("errorCode") != "" {
				copyRaw(&out, "errorCode", m.Get("errorCode"))
			}
			break
		}
	}
	out.Set("state", raw(state))
	out.Set("delivery", raw(delivery))
	return out, nil
}
func (s *Service) Create(ctx context.Context, caller string, input protocol.JSONObject) (protocol.JSONObject, error) {
	if trim(input.String("prompt")) == "" {
		return protocol.JSONObject{}, bad("prompt is required")
	}
	exists, e := s.o.Exists(ctx, caller)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if !exists {
		return protocol.JSONObject{}, missing("Caller session not found")
	}
	ctx = context.WithoutCancel(ctx)
	fence, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		m := s.member(caller, "", nil)
		if truthy(m.Get("stopped")) {
			return protocol.JSONObject{}, conflict("Caller session was stopped; new user input is required")
		}
		epochs := parsed(s.store.Get("stopEpochs"))
		return object("root", m.Get("rootSessionId"), "epoch", num(epochs.Get(m.String("rootSessionId")))), nil
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	key := ""
	if input.String("requestId") != "" {
		key = caller + ":" + input.String("requestId")
	}
	inputBytes, _ := input.MarshalJSON()
	if key != "" {
		previous, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
			creations := parsed(s.store.Get("creations"))
			existing := parsed(creations.Get(key))
			if existing.IsObject() {
				if existing.String("input") != string(inputBytes) {
					return protocol.JSONObject{}, conflict("requestId was reused for different input")
				}
				if f := parsed(existing.Get("failure")); f.IsObject() {
					return protocol.JSONObject{}, &APIError{int(num(f.Get("status"))), f.String("code"), f.String("message")}
				}
				if r := parsed(existing.Get("result")); r.IsObject() {
					return r, nil
				}
				return protocol.JSONObject{}, conflict("Session creation is pending or requires recovery; do not create another session")
			}
			if !creations.IsObject() {
				creations = object()
			}
			creations.Set(key, raw(object("input", string(inputBytes), "rootSessionId", fence.Get("root"), "stopEpoch", fence.Get("epoch"))))
			s.store.Set("creations", raw(creations))
			return object(), nil
		})
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if previous.String("sessionId") != "" {
			return s.describeCreation(ctx, previous)
		}
	}
	created, e := s.o.Create(ctx, caller, clone(input))
	if e != nil {
		var api *APIError
		if !errors.As(e, &api) {
			api = &APIError{500, "SESSION_CREATE_FAILED", e.Error()}
		}
		if key != "" {
			_, save := s.mutate(ctx, func() (protocol.JSONObject, error) {
				creations := parsed(s.store.Get("creations"))
				record := parsed(creations.Get(key))
				record.Set("failure", raw(object("message", api.Message, "code", api.Code, "status", api.Status)))
				creations.Set(key, raw(record))
				s.store.Set("creations", raw(creations))
				return object(), nil
			})
			if save != nil {
				return protocol.JSONObject{}, save
			}
		}
		return protocol.JSONObject{}, api
	}
	title := trim(input.String("title"))
	var titleRaw = raw(title)
	if title == "" {
		titleRaw = stringSlice(input.Get("prompt"), 0, 80)
	}
	message, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		parent := s.member(caller, "", nil)
		m := s.member(created.String("sessionId"), parent.String("rootSessionId"), raw(caller))
		m.Set("title", titleRaw)
		m.Set("stopped", raw(truthy(parent.Get("stopped"))))
		state := "queued"
		if truthy(parent.Get("stopped")) {
			state = "stopped"
		}
		m.Set("state", raw(state))
		s.putMember(m)
		message, e := s.append(caller, created.String("sessionId"), input.Get("prompt"), "assignment", "")
		if e != nil {
			return protocol.JSONObject{}, e
		}
		epochs := parsed(s.store.Get("stopEpochs"))
		if num(epochs.Get(fence.String("root"))) != num(fence.Get("epoch")) {
			message.Set("status", raw("cancelled"))
			m.Set("stopped", raw(true))
			m.Set("state", raw("stopped"))
			s.putMessage(message)
			s.putMember(m)
		}
		if key != "" {
			creations := parsed(s.store.Get("creations"))
			record := parsed(creations.Get(key))
			result := clone(created)
			result.Set("messageId", message.Get("id"))
			result.Set("title", titleRaw)
			record.Set("result", raw(result))
			creations.Set(key, raw(record))
			s.store.Set("creations", raw(creations))
		}
		return message, nil
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if e = s.Pump(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	result := clone(created)
	result.Set("messageId", message.Get("id"))
	result.Set("title", titleRaw)
	return s.describeCreation(ctx, result)
}
func (s *Service) Send(ctx context.Context, caller, target string, content protocol.JSONObject) (protocol.JSONObject, error) {
	if trim(content.String("content")) == "" {
		return protocol.JSONObject{}, bad("content is required")
	}
	exists, e := s.o.Exists(ctx, target)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if !exists {
		return protocol.JSONObject{}, missing("Target session not found")
	}
	ctx = context.WithoutCancel(ctx)
	m, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		if isTrue(s.member(caller, "", nil).Get("stopped")) {
			return protocol.JSONObject{}, conflict("Caller session was stopped; new user input is required")
		}
		s.member(target, "", nil)
		m, e := s.append(caller, target, content.Get("content"), "message", content.String("messageId"))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if m.String("status") == "queued" {
			s.clearError(&m)
			s.putMessage(m)
		}
		return m, nil
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if e = s.Pump(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	r, e := s.Status(ctx, nil)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	for _, actual := range list(r.Get("messages")) {
		if actual.String("id") == m.String("id") {
			return actual, nil
		}
	}
	return protocol.JSONObject{}, errors.New("collaboration message disappeared")
}

// Pump's reentrant caller never joins its own drain. The original dispatcher
// observes mutations made by runtime hooks before looking for its next message.
func (s *Service) Pump(ctx context.Context) error {
	if e := s.await(ctx); e != nil {
		return e
	}
	s.mu.Lock()
	if s.pumping {
		s.mu.Unlock()
		return nil
	}
	s.pumping = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pumping = false; s.mu.Unlock() }()
	for {
		a, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
			members := parsed(s.store.Get("members"))
			for _, message := range list(s.store.Get("messages")) {
				if message.String("status") != "queued" || truthy(message.Get("error")) {
					continue
				}
				member := parsed(members.Get(message.String("targetSessionId")))
				if !member.IsObject() || truthy(member.Get("stopped")) || member.String("state") == "blocked" {
					continue
				}
				if _, ok := s.userAdmissions[member.String("sessionId")]; ok {
					continue
				}
				live := ""
				if s.o.RuntimeState != nil {
					live = s.o.RuntimeState(member.String("sessionId"))
				}
				if live == "blocked" {
					continue
				}
				running := member.String("state") == "running"
				if live != "" {
					running = live == "running"
				}
				workers := 0
				for _, f := range members.OwnProperties() {
					other := parsed(f.Value)
					if other.String("rootSessionId") == member.String("rootSessionId") && other.String("sessionId") != other.String("rootSessionId") && (other.String("state") == "running" || other.String("state") == "blocked") {
						workers++
					}
				}
				if !running && member.String("sessionId") != member.String("rootSessionId") && workers >= 3 {
					continue
				}
				message.Set("status", raw("accepted"))
				member.Set("state", raw("running"))
				s.putMessage(message)
				s.putMember(member)
				return object("message", message, "running", running), nil
			}
			return object(), nil
		})
		if e != nil {
			return e
		}
		message := parsed(a.Get("message"))
		if !message.IsObject() {
			return nil
		}
		if isTrue(a.Get("running")) {
			e = s.o.Enqueue(ctx, message.String("targetSessionId"), message)
		} else {
			e = s.o.Start(ctx, message.String("targetSessionId"), message)
		}
		if e == nil {
			continue
		}
		runtimeFailure := e
		_, e = s.mutate(ctx, func() (protocol.JSONObject, error) {
			var current protocol.JSONObject
			for _, m := range list(s.store.Get("messages")) {
				if m.String("id") == message.String("id") {
					current = m
					break
				}
			}
			if current.String("status") == "cancelled" || current.String("status") == "consumed" {
				return object(), nil
			}
			current.Set("status", raw("queued"))
			s.putMessage(current)
			member := parsed(parsed(s.store.Get("members")).Get(current.String("targetSessionId")))
			if truthy(member.Get("stopped")) {
				return object(), nil
			}
			current.Set("error", raw(runtimeFailure.Error()))
			if code := errorCode(runtimeFailure); code != "" {
				current.Set("errorCode", raw(code))
			}
			s.putMessage(current)
			if !isTrue(a.Get("running")) {
				member.Set("state", raw("failed"))
				s.putMember(member)
				if member.String("rootSessionId") != member.String("sessionId") {
					_, e := s.append(member.String("sessionId"), member.String("rootSessionId"), raw("Unable to start session "+member.String("sessionId")+": "+current.String("error")), "failed", "")
					if e != nil {
						return protocol.JSONObject{}, e
					}
				}
			}
			return object(), nil
		})
		if e != nil {
			return e
		}
	}
}
