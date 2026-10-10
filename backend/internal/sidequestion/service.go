// Package sidequestion owns only ephemeral /btw control work. A side question
// does not enqueue a main user turn, change its abort controller, or persist it.
package sidequestion

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Status        int
	Code, Message string
	MessageJSON   json.RawMessage
}

func (e *Error) Error() string { return e.Message }
func (e *Error) ErrorMessageJSON() json.RawMessage {
	if e.MessageJSON != nil {
		return e.MessageJSON
	}
	return quote(e.Message)
}

type Options struct {
	Lifetime context.Context
	Ensure   func(context.Context, string, *url.URL) error
	Control  func(context.Context, string, protocol.JSONObject, time.Duration) (protocol.JSONObject, error)
	Timeout  time.Duration
}
type pending struct {
	id                         string
	ctx                        context.Context
	cancel                     context.CancelFunc
	started, timedOut, aborted bool
	stopOnce                   sync.Once
	stopped                    chan struct{}
}
type remembered struct {
	ids   map[string]bool
	order []string
}
type Service struct {
	mu        sync.Mutex
	opts      Options
	life      context.Context
	cancel    context.CancelFunc
	pending   map[string]*pending
	seen      map[string]*remembered
	work      sync.WaitGroup
	closed    bool
	closeDone chan struct{}
}

func New(o Options) (*Service, error) {
	if o.Lifetime == nil || o.Ensure == nil || o.Control == nil {
		return nil, errors.New("side questions require owned lifetime, actual CLI startup and control")
	}
	if o.Timeout == 0 {
		o.Timeout = 300 * time.Second
	}
	ctx, cancel := context.WithCancel(o.Lifetime)
	return &Service{opts: o, life: ctx, cancel: cancel, pending: map[string]*pending{}, seen: map[string]*remembered{}}, nil
}
func (s *Service) remember(id, q string) {
	r := s.seen[id]
	if r == nil {
		r = &remembered{ids: map[string]bool{}}
		s.seen[id] = r
	}
	if !r.ids[q] {
		r.ids[q] = true
		r.order = append(r.order, q)
	}
	if len(r.order) > 1000 {
		delete(r.ids, r.order[0])
		r.order = r.order[1:]
	}
}
func (s *Service) abort(id string, p *pending) {
	p.stopOnce.Do(func() {
		s.mu.Lock()
		p.cancel()
		p.aborted = true
		started := p.started
		if started {
			s.work.Add(1)
		}
		s.mu.Unlock()
		if !started {
			close(p.stopped)
			return
		}
		go func() {
			defer s.work.Done()
			defer close(p.stopped)
			_, _ = s.opts.Control(s.life, id, protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("cancel_side_question")}, protocol.JSONField{Name: "question_id", Value: quote(p.id)}), 10*time.Second)
		}()
	})
}
func (s *Service) Cancel(id, q string) (bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false, context.Canceled
	}
	p := s.pending[id]
	if p == nil || p.id != q {
		if ValidUUID(q) {
			s.remember(id, q)
		}
		s.mu.Unlock()
		return false, nil
	}
	s.mu.Unlock()
	s.abort(id, p)
	<-p.stopped
	return true, nil
}
func (s *Service) Ask(signal context.Context, id string, in Input, address *url.URL) (protocol.JSONObject, error) {
	if signal.Err() != nil {
		return protocol.JSONObject{}, &Error{Status: 499, Code: "SIDE_QUESTION_CANCELLED", Message: "Side question cancelled"}
	}
	s.mu.Lock()
	if s.closed || s.life.Err() != nil {
		s.mu.Unlock()
		return protocol.JSONObject{}, context.Canceled
	}
	if s.pending[id] != nil {
		s.mu.Unlock()
		return protocol.JSONObject{}, &Error{Status: 409, Code: "CONFLICT", Message: "A side question is already running in this session"}
	}
	if r := s.seen[id]; r != nil && r.ids[in.QuestionID] {
		s.mu.Unlock()
		return protocol.JSONObject{}, &Error{Status: 409, Code: "CONFLICT", Message: "This side question ID has already been used"}
	}
	s.remember(id, in.QuestionID)
	ctx, cancel := context.WithCancel(s.life)
	p := &pending{id: in.QuestionID, ctx: ctx, cancel: cancel, stopped: make(chan struct{})}
	s.pending[id] = p
	s.work.Add(1)
	s.mu.Unlock()
	relay := context.AfterFunc(signal, func() { s.abort(id, p) })
	timer := time.AfterFunc(s.opts.Timeout, func() { s.mu.Lock(); p.timedOut = true; s.mu.Unlock(); s.abort(id, p) })
	defer func() {
		timer.Stop()
		relay()
		cancel()
		// Only explicit cancellation installs a cancel control job. Ordinary
		// success must not manufacture a cancel_side_question request.
		s.mu.Lock()
		stopped := p.aborted || s.life.Err() != nil
		s.mu.Unlock()
		if stopped {
			s.abort(id, p)
			<-p.stopped
		}
		s.mu.Lock()
		if s.pending[id] == p {
			delete(s.pending, id)
		}
		s.mu.Unlock()
		s.work.Done()
	}()
	ensure := make(chan error, 1)
	s.work.Add(1)
	go func() { defer s.work.Done(); ensure <- s.opts.Ensure(s.life, id, address) }()
	var err error
	select {
	case err = <-ensure:
	case <-p.ctx.Done():
		err = context.Cause(p.ctx)
	}
	if err == nil {
		s.mu.Lock()
		if p.ctx.Err() == nil {
			p.started = true
		} else {
			err = context.Cause(p.ctx)
		}
		s.mu.Unlock()
	}
	var result protocol.JSONObject
	if err == nil {
		request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("side_question")}, protocol.JSONField{Name: "question_id", Value: quote(in.QuestionID)}, protocol.JSONField{Name: "question", Value: in.Question}, protocol.JSONField{Name: "history", Value: in.History})
		type controlResult struct {
			value protocol.JSONObject
			err   error
		}
		control := make(chan controlResult, 1)
		s.work.Add(1)
		go func() {
			defer s.work.Done()
			value, e := s.opts.Control(p.ctx, id, request, s.opts.Timeout)
			control <- controlResult{value, e}
		}()
		select {
		case outcome := <-control:
			result, err = outcome.value, outcome.err
		case <-p.ctx.Done():
			err = context.Cause(p.ctx)
		}
		if err == nil {
			if _, e := protocol.DecodeJavaScriptJSONString(result.Get("response")); e != nil {
				err = errors.New("CLI returned an invalid side question response")
			}
		}
	}
	s.mu.Lock()
	aborted, timedOut := p.ctx.Err() != nil, p.timedOut
	s.mu.Unlock()
	if aborted {
		if timedOut {
			return protocol.JSONObject{}, &Error{Status: 504, Code: "SIDE_QUESTION_TIMEOUT", Message: "Side question timed out"}
		}
		return protocol.JSONObject{}, &Error{Status: 499, Code: "SIDE_QUESTION_CANCELLED", Message: "Side question cancelled"}
	}
	if err != nil {
		var api *Error
		if errors.As(err, &api) {
			return protocol.JSONObject{}, api
		}
		return protocol.JSONObject{}, &Error{Status: 502, Code: "SIDE_QUESTION_FAILED", Message: err.Error(), MessageJSON: ErrorMessageJSON(err)}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "questionId", Value: quote(in.QuestionID)}, protocol.JSONField{Name: "response", Value: result.Get("response")}), nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closeDone == nil {
		s.closed = true
		s.closeDone = make(chan struct{})
		entries := map[string]*pending{}
		for id, p := range s.pending {
			entries[id] = p
		}
		s.cancel()
		go func() {
			for id, p := range entries {
				s.abort(id, p)
			}
			s.work.Wait()
			close(s.closeDone)
		}()
	}
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func quote(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
