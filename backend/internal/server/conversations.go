package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/conversationapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) newConversationAPI() error {
	service, e := conversationapi.New(conversationapi.Options{Exists: func(ctx context.Context, id string) (bool, error) {
		_, e := s.findSession(ctx, id)
		var api *apiError
		if errors.As(e, &api) && api.status == 404 {
			return false, nil
		}
		return e == nil, e
	}, UUID: uuid, Activity: s.conversationActivity})
	if e == nil {
		s.conversations = service
	}
	return e
}
func (s *Server) conversationsHTTP(w http.ResponseWriter, r *http.Request, parts []string) error {
	status, value, e := s.conversations.Handle(r.Context(), r.Method, parts, r.Body)
	var api *conversationapi.Error
	if errors.As(e, &api) {
		return &apiError{api.Status, api.Code, api.Message}
	}
	if e != nil {
		return e
	}
	writeJSON(w, status, value)
	return nil
}
func (s *Server) conversationActivity(ctx context.Context, id string) (conversationapi.Activity, error) {
	s.mu.Lock()
	runtime := s.runtimes[id]
	s.mu.Unlock()
	activity := conversationapi.Activity{}
	if runtime == nil {
		return activity, nil
	}
	runtime.mu.Lock()
	live := runtime.live
	activity.Running = runtime.admitting || len(runtime.admission) > 0
	runtime.mu.Unlock()
	if live == nil {
		return activity, nil
	}
	frames, e := live.Snapshot(ctx)
	if e != nil {
		return activity, e
	}
	for _, raw := range frames {
		var frame protocol.JSONObject
		if frame.UnmarshalJSON(raw) != nil {
			continue
		}
		if frame.String("type") == "permission_request" {
			activity.Waiting = true
		}
		if frame.String("type") == "session_state" {
			activity.Running = activity.Running || frame.String("turnState") == "running"
			var ids []string
			_ = json.Unmarshal(frame.Get("activeBackgroundTaskIds"), &ids)
			activity.Running = activity.Running || len(ids) > 0
		}
	}
	return activity, nil
}
func (s *Server) observeConversationSDK(id string, data json.RawMessage) {
	s.observeSDKCapture(id, data)
	var frame protocol.JSONObject
	if frame.UnmarshalJSON(data) != nil {
		return
	}
	if frame.String("type") == "system" && frame.String("subtype") == "session_state_changed" {
		if state := frame.String("state"); state == "running" || state == "idle" {
			s.conversations.RunState(id, state == "running")
		}
	}
	if frame.String("type") == "result" {
		s.conversations.Finish(id, sourceJSONTruthy(frame.Get("is_error")))
	}
}
