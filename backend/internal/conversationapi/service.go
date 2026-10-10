// Package conversationapi preserves Source's legacy REST queue/status contract.
// Model execution and interrupt remain the shared WebSocket/Harness consumers.
package conversationapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

type Activity struct{ Waiting, Running, Interrupted bool }
type Options struct {
	Exists   func(context.Context, string) (bool, error)
	Activity func(context.Context, string) (Activity, error)
	UUID     func() (string, error)
}
type State struct {
	Legacy         string
	Queued, Failed bool
	Interrupted    bool
	SDKRunning     bool
}
type Service struct {
	options Options
	mu      sync.Mutex
	states  map[string]State
}

func New(o Options) (*Service, error) {
	if o.Exists == nil || o.Activity == nil || o.UUID == nil {
		return nil, errors.New("conversation API requires actual session summary/activity/UUID producers")
	}
	return &Service{options: o, states: map[string]State{}}, nil
}
func (s *Service) SetLegacy(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.states[id]
	v.Legacy = state
	v.Failed = false
	v.Interrupted = false
	v.Queued = state != "idle"
	s.states[id] = v
}
func (s *Service) Begin(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.states[id]
	v.Failed, v.Queued = false, false
	v.Interrupted = false
	s.states[id] = v
}
func (s *Service) Finish(id string, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.states[id]
	v.Queued = false
	v.Failed = failed && !v.Interrupted
	s.states[id] = v
}
func (s *Service) Status(ctx context.Context, id string) (protocol.JSONObject, error) {
	activity, e := s.options.Activity(ctx, id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	s.mu.Lock()
	v := s.states[id]
	s.mu.Unlock()
	legacy := v.Legacy
	if legacy == "" {
		legacy = "idle"
	}
	state := "idle"
	if !activity.Interrupted && !v.Interrupted {
		if activity.Waiting {
			state = "waiting"
		} else if activity.Running || v.SDKRunning {
			state = "running"
		} else if v.Failed {
			state = "failed"
		} else if v.Queued {
			state = "running"
		}
	}
	return obj(field("state", legacy), field("activityState", state)), nil
}
func (s *Service) Handle(ctx context.Context, method string, parts []string, body io.Reader) (int, protocol.JSONObject, error) {
	id, action := "", ""
	if len(parts) > 2 {
		id = parts[2]
	}
	index := 3
	if len(parts) > 1 && parts[1] == "sessions" {
		index = 4
	}
	if len(parts) > index {
		action = parts[index]
	}
	if id == "" {
		return 0, protocol.JSONObject{}, &Error{400, "BAD_REQUEST", "Session ID is required"}
	}
	if action == "status" && method == http.MethodGet {
		value, e := s.Status(ctx, id)
		return 200, value, e
	}
	if action == "stop" && method == http.MethodPost {
		s.SetLegacy(id, "idle")
		return 200, obj(field("ok", true)), nil
	}
	if action != "" {
		return 404, obj(field("error", "NOT_FOUND"), field("message", "Unknown chat sub-resource: "+action)), nil
	}
	if method != http.MethodPost {
		return 405, obj(field("error", "METHOD_NOT_ALLOWED"), field("message", "Method "+method+" not allowed")), nil
	}
	exists, e := s.options.Exists(ctx, id)
	if e != nil {
		return 0, protocol.JSONObject{}, e
	}
	if !exists {
		return 0, protocol.JSONObject{}, &Error{404, "NOT_FOUND", "Session not found: " + id}
	}
	data, e := io.ReadAll(body)
	if e != nil {
		return 0, protocol.JSONObject{}, &Error{400, "BAD_REQUEST", "Invalid JSON body"}
	}
	if !json.Valid(data) {
		return 0, protocol.JSONObject{}, &Error{400, "BAD_REQUEST", "Invalid JSON body"}
	}
	if string(data) == "null" {
		return 0, protocol.JSONObject{}, &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	var record protocol.JSONObject
	if e = record.UnmarshalJSON(data); e != nil {
		return 0, protocol.JSONObject{}, &Error{400, "BAD_REQUEST", "content (string) is required in request body"}
	}
	if record.String("content") == "" {
		return 0, protocol.JSONObject{}, &Error{400, "BAD_REQUEST", "content (string) is required in request body"}
	}
	messageID, e := s.options.UUID()
	if e != nil {
		return 0, protocol.JSONObject{}, e
	}
	s.SetLegacy(id, "thinking")
	return 202, obj(field("messageId", messageID), field("status", "queued")), nil
}

func (s *Service) Interrupt(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.states[id]
	v.Queued, v.Failed, v.Interrupted = false, false, true
	s.states[id] = v
}
func (s *Service) RunState(id string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.states[id]
	v.SDKRunning = running
	if running {
		v.Queued, v.Failed, v.Interrupted = false, false, false
	}
	s.states[id] = v
}
func field(name string, value any) protocol.JSONField {
	b, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: b}
}
func obj(fields ...protocol.JSONField) protocol.JSONObject { return protocol.NewJSONObject(fields...) }
