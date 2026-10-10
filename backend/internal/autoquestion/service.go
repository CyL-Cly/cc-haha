package autoquestion

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Timer interface{ Stop() bool }
type Options struct {
	ReadSettings func(context.Context) (json.RawMessage, error)
	Decider      Decider
	Now          func() time.Time
	AfterFunc    func(time.Duration, func()) Timer
	Log          func(string, error)
}
type Binding struct {
	ReadContext             func(context.Context, string) (json.RawMessage, error)
	ProviderID              *string
	UsesOfficialOAuth       bool
	ProviderFingerprint     json.RawMessage
	ReadProviderFingerprint func(context.Context, string) (json.RawMessage, error)
	Resolve                 func(context.Context, string, json.RawMessage) (bool, error)
}
type Request struct {
	ID, ToolName, AgentID string
	Input                 json.RawMessage
}
type pending struct {
	request    Request
	createdAt  time.Time
	generation uint64
	cancelled  bool
	timer      Timer
	cancel     context.CancelFunc
}
type session struct {
	binding  Binding
	requests map[string]*pending
	ctx      context.Context
	cancel   context.CancelFunc
}
type Service struct {
	options  Options
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
	wg       sync.WaitGroup
}

func New(o Options) (*Service, error) {
	if o.ReadSettings == nil || o.Decider.Resolve == nil {
		return nil, errors.New("automatic questions require actual settings and model target producers")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AfterFunc == nil {
		o.AfterFunc = func(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{options: o, ctx: ctx, cancel: cancel, sessions: map[string]*session{}}, nil
}
func clearWait(p *pending) {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}
func (s *Service) Bind(id string, b Binding) func() {
	b.ProviderFingerprint = append(json.RawMessage(nil), b.ProviderFingerprint...)
	if b.ProviderID != nil {
		id := *b.ProviderID
		b.ProviderID = &id
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return func() {}
	}
	if old := s.sessions[id]; old != nil {
		old.cancel()
		for _, p := range old.requests {
			clearWait(p)
		}
	}
	ctx, cancel := context.WithCancel(s.ctx)
	current := &session{binding: b, requests: map[string]*pending{}, ctx: ctx, cancel: cancel}
	s.sessions[id] = current
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.sessions[id] == current {
			delete(s.sessions, id)
			current.cancel()
			for _, p := range current.requests {
				clearWait(p)
			}
		}
		s.mu.Unlock()
	}
}
func (s *Service) Track(id string, r Request) {
	if r.ToolName != "AskUserQuestion" {
		return
	}
	r.Input = append(json.RawMessage(nil), r.Input...)
	s.mu.Lock()
	current := s.sessions[id]
	if s.closed || current == nil {
		s.mu.Unlock()
		return
	}
	if old := current.requests[r.ID]; old != nil {
		clearWait(old)
	}
	p := &pending{request: r, createdAt: s.options.Now()}
	current.requests[r.ID] = p
	s.scheduleLocked(id, current, p)
	s.mu.Unlock()
}
func (s *Service) Remove(id, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.sessions[id]; current != nil {
		if p := current.requests[requestID]; p != nil {
			clearWait(p)
			delete(current.requests, requestID)
		}
	}
}
func (s *Service) CancelActivity(id, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.sessions[id]; current != nil {
		if p := current.requests[requestID]; p != nil {
			clearWait(p)
			p.cancelled = true
			p.generation++
		}
	}
}
func (s *Service) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for id, current := range s.sessions {
		for _, p := range current.requests {
			if p.cancelled {
				continue
			}
			clearWait(p)
			p.generation++
			s.scheduleLocked(id, current, p)
		}
	}
}
func (s *Service) active(id string, current *session, p *pending, g uint64) bool {
	return !s.closed && s.sessions[id] == current && current.requests[p.request.ID] == p && !p.cancelled && p.generation == g && current.ctx.Err() == nil
}
func (s *Service) scheduleLocked(id string, current *session, p *pending) {
	g := p.generation
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		data, e := s.options.ReadSettings(current.ctx)
		if e != nil {
			s.log("cannot schedule", e)
			return
		}
		settings := NormalizeSettings(data)
		s.mu.Lock()
		defer s.mu.Unlock()
		if !settings.Enabled || !s.active(id, current, p, g) {
			return
		}
		remaining := p.createdAt.Add(time.Duration(settings.TimeoutMinutes) * time.Minute).Sub(s.options.Now())
		if remaining < 0 {
			remaining = 0
		}
		p.timer = s.options.AfterFunc(remaining, func() {
			s.mu.Lock()
			if !s.active(id, current, p, g) {
				s.mu.Unlock()
				return
			}
			p.timer = nil
			ctx, cancel := context.WithCancel(current.ctx)
			p.cancel = cancel
			s.wg.Add(1)
			s.mu.Unlock()
			defer s.wg.Done()
			defer cancel()
			s.answer(ctx, id, current, p, g)
		})
	}()
}
func (s *Service) log(reason string, e error) {
	if s.options.Log != nil {
		s.options.Log(reason, e)
	}
}
func (s *Service) answer(ctx context.Context, id string, current *session, p *pending, g uint64) {
	defer func() {
		s.mu.Lock()
		if s.active(id, current, p, g) {
			p.cancel = nil
		}
		s.mu.Unlock()
	}()
	settings, e := s.options.ReadSettings(ctx)
	if e != nil || !NormalizeSettings(settings).Enabled {
		return
	}
	questions := ParseQuestions(p.request.Input)
	if questions == nil {
		return
	}
	if current.binding.ReadContext == nil || current.binding.Resolve == nil {
		s.log("missing context/permission producer", errors.New("automatic question runtime is not bound"))
		return
	}
	conversation, e := current.binding.ReadContext(ctx, p.request.AgentID)
	if e != nil || !nonblank(conversation) {
		return
	}
	provider := current.binding.ProviderID
	if provider == nil && !current.binding.UsesOfficialOAuth {
		return
	}
	if provider != nil && current.binding.ProviderFingerprint != nil {
		if current.binding.ReadProviderFingerprint == nil {
			return
		}
		currentFingerprint, e := current.binding.ReadProviderFingerprint(ctx, *provider)
		if e != nil {
			return
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(currentFingerprint)) != string(protocol.StringifyValidatedJavaScriptJSON(current.binding.ProviderFingerprint)) {
			s.log("provider_changed", nil)
			return
		}
	}
	answers, e := s.options.Decider.Decide(ctx, DecisionInput{Questions: questions, Conversation: conversation, ProviderID: provider, SessionID: id})
	if e != nil {
		s.log("decision failed", e)
		return
	}
	if answers == nil || ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	valid := s.active(id, current, p, g)
	s.mu.Unlock()
	if !valid {
		return
	}
	settings, e = s.options.ReadSettings(ctx)
	if e != nil || !NormalizeSettings(settings).Enabled || ctx.Err() != nil {
		return
	}
	updated := object(p.request.Input).Clone()
	updated.Set("answers", encode(*answers))
	metadata := object(updated.Get("metadata"))
	if !metadata.IsObject() {
		metadata = protocol.NewJSONObject()
	}
	metadata.Set("autoAnswered", []byte("true"))
	updated.Set("metadata", encode(metadata))
	resolved, e := current.binding.Resolve(ctx, p.request.ID, encode(updated))
	if e != nil {
		s.log("resolve failed", e)
		return
	}
	if resolved {
		s.Remove(id, p.request.ID)
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, current := range s.sessions {
		current.cancel()
		for _, p := range current.requests {
			clearWait(p)
		}
	}
	clear(s.sessions)
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// ConversationText matches the original projected history flattening, including
// JavaScript UTF-16 slicing. Tool results and sidechains are not parent context.
func ConversationText(messages []json.RawMessage, includeSidechain bool) json.RawMessage {
	parts := [][]uint16{}
	for _, raw := range messages {
		m := object(raw)
		kind := m.String("type")
		if kind != "user" && kind != "assistant" {
			continue
		}
		if !includeSidechain && (truthy(m.Get("isSidechain")) || truthy(m.Get("parentToolUseId"))) {
			continue
		}
		content := m.Get("content")
		if u, e := protocol.DecodeJavaScriptJSONString(content); e == nil {
			parts = append(parts, append(utf16.Encode([]rune(kind+": ")), u...))
			continue
		}
		var blocks []json.RawMessage
		if json.Unmarshal(content, &blocks) != nil {
			continue
		}
		text := []uint16{}
		count := 0
		for _, block := range blocks {
			v := object(block)
			if v.String("type") != "text" {
				continue
			}
			u, e := protocol.DecodeJavaScriptJSONString(v.Get("text"))
			if e != nil {
				continue
			}
			if count > 0 {
				text = append(text, '\n')
			}
			text = append(text, u...)
			count++
		}
		if len(text) > 0 {
			parts = append(parts, append(utf16.Encode([]rune(kind+": ")), text...))
		}
	}
	all := []uint16{}
	for i, p := range parts {
		if i > 0 {
			all = append(all, '\n')
		}
		all = append(all, p...)
	}
	if len(all) > 4000 {
		all = all[len(all)-4000:]
	}
	return protocol.QuoteJavaScriptUTF16(all)
}
func truthy(data json.RawMessage) bool {
	if len(data) == 0 || string(data) == "null" || string(data) == "false" || string(data) == `""` || string(data) == "0" {
		return false
	}
	return true
}
func CombineContext(root, agent json.RawMessage, hasAgent bool) json.RawMessage {
	if !hasAgent {
		return root
	}
	if !nonblank(agent) {
		return encode("")
	}
	r, a := units(root), units(agent)
	if len(r) > 2000 {
		r = r[len(r)-2000:]
	}
	if len(a) > 2000 {
		a = a[len(a)-2000:]
	}
	return protocol.QuoteJavaScriptUTF16(append(append(r, '\n'), a...))
}
