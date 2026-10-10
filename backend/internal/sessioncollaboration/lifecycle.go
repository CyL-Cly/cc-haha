package sessioncollaboration

import (
	"context"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) incrementInput(id string) { s.mu.Lock(); s.userInputs[id]++; s.mu.Unlock() }
func (s *Service) notify()                  { s.mu.Lock(); s.notifyLocked(); s.mu.Unlock() }
func (s *Service) OnStopped(ctx context.Context, id string) error {
	s.incrementInput(id)
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		delete(s.userAdmissions, id)
		m := s.member(id, "", nil)
		m.Set("stopped", raw(true))
		m.Set("state", raw("stopped"))
		s.putMember(m)
		for _, message := range list(s.store.Get("messages")) {
			if message.String("targetSessionId") == id && message.String("status") == "accepted" {
				message.Set("status", raw("queued"))
				s.clearError(&message)
				s.putMessage(message)
			}
		}
		return object(), nil
	})
	if e != nil {
		return e
	}
	s.notify()
	return nil
}
func (s *Service) Stop(ctx context.Context, id string) error {
	if e := s.OnStopped(ctx, id); e != nil {
		return e
	}
	if e := s.o.Stop(ctx, id); e != nil {
		return e
	}
	return s.Pump(ctx)
}
func (s *Service) StopGroup(ctx context.Context, id string) error {
	out, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		root := s.member(id, "", nil).String("rootSessionId")
		epochs := parsed(s.store.Get("stopEpochs"))
		if !epochs.IsObject() {
			epochs = object()
		}
		epochs.Set(root, raw(num(epochs.Get(root))+1))
		s.store.Set("stopEpochs", raw(epochs))
		names := []string{}
		targets := map[string]bool{}
		members := parsed(s.store.Get("members"))
		for _, f := range members.OwnProperties() {
			m := parsed(f.Value)
			if m.String("rootSessionId") == root {
				sid := m.String("sessionId")
				names = append(names, sid)
				targets[sid] = true
				delete(s.userAdmissions, sid)
				m.Set("stopped", raw(true))
				m.Set("state", raw("stopped"))
				s.putMember(m)
			}
		}
		for _, m := range list(s.store.Get("messages")) {
			if targets[m.String("targetSessionId")] && (m.String("status") == "queued" || m.String("status") == "accepted") {
				m.Set("status", raw("cancelled"))
				s.putMessage(m)
			}
		}
		return object("ids", names), nil
	})
	if e != nil {
		return e
	}
	var names []string
	_ = decode(out.Get("ids"), &names)
	for _, sid := range names {
		s.incrementInput(sid)
	}
	s.notify()
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	pending := make([]<-chan error, 0, len(names))
	for _, sid := range names {
		pending = append(pending, s.o.BeginStop(ctx, sid))
	}
	for _, future := range pending {
		wg.Add(1)
		go func(done <-chan error) {
			defer wg.Done()
			if <-done != nil {
				mu.Lock()
				failures++
				mu.Unlock()
			}
		}(future)
	}
	wg.Wait()
	if failures > 0 {
		return conflict(fmt.Sprintf("Stopped collaboration wakeups, but %d runtime stop request(s) failed", failures))
	}
	return nil
}
func (s *Service) Resume(ctx context.Context, id string) error {
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		m := s.member(id, "", nil)
		m.Set("stopped", raw(false))
		m.Set("state", raw("idle"))
		s.putMember(m)
		for _, message := range list(s.store.Get("messages")) {
			if message.String("targetSessionId") == id {
				s.clearError(&message)
				s.putMessage(message)
			}
		}
		return object(), nil
	})
	if e != nil {
		return e
	}
	return s.Pump(ctx)
}

type Reservation struct {
	service   *Service
	sessionID string
	token     *struct{ value byte }
	previous  protocol.JSONObject
}

func (s *Service) AdmitUserTurn(ctx context.Context, id string, canAdmit func() bool) (*Reservation, error) {
	token := &struct{ value byte }{}
	previous, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		if canAdmit != nil && !canAdmit() {
			return protocol.JSONObject{}, conflict("Session turn was cancelled before admission")
		}
		m := s.member(id, "", nil)
		previous := object("state", m.Get("state"), "stopped", m.Get("stopped"))
		if p, ok := s.userAdmissions[id]; ok {
			previous = clone(p.previous)
		}
		workers := 0
		for _, f := range parsed(s.store.Get("members")).OwnProperties() {
			other := parsed(f.Value)
			if other.String("rootSessionId") == m.String("rootSessionId") && other.String("sessionId") != other.String("rootSessionId") && other.String("sessionId") != id && (other.String("state") == "running" || other.String("state") == "blocked") {
				workers++
			}
		}
		if m.String("sessionId") != m.String("rootSessionId") && workers >= 3 {
			return protocol.JSONObject{}, &APIError{409, "SESSION_WORKER_CAPACITY", "All three worker slots are occupied. Wait for a worker to finish before sending this turn."}
		}
		m.Set("state", raw("running"))
		m.Set("stopped", raw(false))
		s.putMember(m)
		s.userAdmissions[id] = admission{token, previous}
		return previous, nil
	})
	if e != nil {
		return nil, e
	}
	return &Reservation{s, id, token, previous}, nil
}
func (r *Reservation) Release(ctx context.Context) error {
	s := r.service
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		current, ok := s.userAdmissions[r.sessionID]
		if !ok || current.token != r.token {
			return object(), nil
		}
		delete(s.userAdmissions, r.sessionID)
		m := parsed(parsed(s.store.Get("members")).Get(r.sessionID))
		if m.String("state") == "running" {
			m.Assign(r.previous)
			s.putMember(m)
		}
		return object(), nil
	})
	if e != nil {
		return e
	}
	return s.Pump(ctx)
}
func (s *Service) OnUserInput(ctx context.Context, id string, dispatch bool) error {
	s.incrementInput(id)
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		m := s.member(id, "", nil)
		m.Set("stopped", raw(false))
		m.Set("state", raw("running"))
		s.putMember(m)
		for _, message := range list(s.store.Get("messages")) {
			if message.String("targetSessionId") == id && message.String("status") == "queued" {
				s.clearError(&message)
				s.putMessage(message)
			}
		}
		return object(), nil
	})
	if e != nil {
		return e
	}
	s.notify()
	if dispatch {
		return s.Pump(ctx)
	}
	return nil
}
func (s *Service) OnMessageConsumed(ctx context.Context, messageID, target string) error {
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		for _, m := range list(s.store.Get("messages")) {
			if m.String("id") == messageID && m.String("status") != "cancelled" && (target == "" || m.String("targetSessionId") == target) {
				m.Set("status", raw("consumed"))
				s.putMessage(m)
				break
			}
		}
		return object(), nil
	})
	return e
}
func (s *Service) CanDeliver(messageID string) bool {
	select {
	case <-s.ready:
	default:
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadError != nil || s.closed {
		return false
	}
	for _, m := range list(s.store.Get("messages")) {
		if m.String("id") == messageID {
			return m.String("status") == "accepted" && !truthy(parsed(parsed(s.store.Get("members")).Get(m.String("targetSessionId"))).Get("stopped"))
		}
	}
	return false
}
func (s *Service) OnSessionState(ctx context.Context, id, state string, result protocol.JSONObject, eventID string) error {
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		delete(s.userAdmissions, id)
		m := s.member(id, "", nil)
		changed := m.String("state") != state || !optionalStringEqual(m.Get("result"), result.Get("result"))
		if !truthy(m.Get("stopped")) {
			m.Set("state", raw(state))
		}
		copyRaw(&m, "result", result.Get("result"))
		s.putMember(m)
		if changed && !truthy(m.Get("stopped")) && m.String("rootSessionId") != id && (state == "completed" || state == "blocked" || state == "failed") {
			content := result.Get("result")
			if result.String("result") == "" {
				content = raw("Session " + id + " is " + state + ".")
			}
			_, e := s.append(id, m.String("rootSessionId"), content, state, eventID)
			if e != nil {
				return protocol.JSONObject{}, e
			}
		}
		return object(), nil
	})
	if e != nil {
		return e
	}
	return s.Pump(ctx)
}
func (s *Service) Recover(ctx context.Context) error {
	_, e := s.mutate(ctx, func() (protocol.JSONObject, error) {
		messages := list(s.store.Get("messages"))
		for _, f := range parsed(s.store.Get("members")).OwnProperties() {
			m := parsed(f.Value)
			pending := false
			for _, message := range messages {
				if message.String("targetSessionId") == m.String("sessionId") && (message.String("status") == "queued" || message.String("status") == "accepted") {
					pending = true
					break
				}
			}
			if m.String("state") == "running" || m.String("state") == "queued" || m.String("state") == "blocked" || pending {
				m.Set("state", raw("stopped"))
				m.Set("stopped", raw(true))
				s.putMember(m)
			}
		}
		for _, m := range messages {
			if m.String("status") == "accepted" {
				m.Set("status", raw("queued"))
				s.putMessage(m)
			}
		}
		return object(), nil
	})
	return e
}
