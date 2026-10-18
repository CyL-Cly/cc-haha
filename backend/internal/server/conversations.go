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
	available := !s.closed && !s.migration.active && !s.deleting[id]
	s.mu.Unlock()
	activity := conversationapi.Activity{}
	var runtimeGeneration uint64
	leadPresent := false
	if !available {
		return activity, nil
	}
	if runtime != nil {
		runtime.mu.Lock()
		activity = runtimeConversationActivity(runtime)
		live := runtime.live
		generation := runtime.disconnectState().generation
		runtimeGeneration = generation
		leadPresent = !runtime.retired && runtime.ctx.Err() == nil && runtime.closing == nil && (runtime.live != nil || runtime.opening != nil)
		if runtime.retired || runtime.ctx.Err() != nil || runtime.closing != nil {
			live = nil
		}
		runtime.mu.Unlock()
		if live != nil {
			// Snapshot owns pending tool/CU approvals. Its foreground projection
			// and UI task IDs are not the server's registered-turn/task owner.
			frames, err := live.Snapshot(ctx)
			s.mu.Lock()
			owned := !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == runtime
			s.mu.Unlock()
			runtime.mu.Lock()
			same := owned && !runtime.retired && runtime.ctx.Err() == nil && runtime.closing == nil && runtime.live == live && runtime.disconnectState().generation == generation
			activity = runtimeConversationActivity(runtime)
			runtime.mu.Unlock()
			if !owned {
				return conversationapi.Activity{}, nil
			}
			if same {
				if err != nil {
					return activity, err
				}
				for _, raw := range frames {
					var frame protocol.JSONObject
					if frame.UnmarshalJSON(raw) == nil && (frame.String("type") == "permission_request" || frame.String("type") == "computer_use_permission_request") {
						activity.Waiting = true
					}
				}
			}
		}
	}
	// The Source pending-tool producer rolls child requests up to the lead.
	// Team work alone and a child's CU approvals do not make the lead busy.
	// Source returns no rolled-up requests when the lead registry is absent.
	if !leadPresent {
		return activity, nil
	}
	children, err := s.teamPermissionSnapshots(ctx, id)
	if err != nil {
		return activity, err
	}
	s.mu.Lock()
	owned := !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == runtime
	s.mu.Unlock()
	runtime.mu.Lock()
	same := owned && !runtime.retired && runtime.ctx.Err() == nil && runtime.closing == nil && runtime.disconnectState().generation == runtimeGeneration && (runtime.live != nil || runtime.opening != nil)
	runtime.mu.Unlock()
	if !same {
		return conversationapi.Activity{}, nil
	}
	activity.Waiting = activity.Waiting || len(children) > 0
	return activity, nil
}

// Caller holds runtime.mu. These are actual registered/SDK/control owners,
// including confirmed Agent bookends while persistence is in flight.
func runtimeConversationActivity(runtime *runtimeSession) conversationapi.Activity {
	activity := conversationapi.Activity{}
	if runtime.retired || runtime.ctx.Err() != nil {
		return activity
	}
	d := runtime.disconnectState()
	activity.Running = hasRegisteredUserTurn(runtime)
	if runtime.live != nil || runtime.opening != nil {
		activity.Running = activity.Running || d.cliRunning
		activity.Waiting = len(d.tool) > 0 || len(d.computer) > 0
	}
	activity.Running = activity.Running || d.hasActiveTasks()
	return activity
}
func (s *Server) observeConversationSDK(id string, data json.RawMessage) {
	s.observeSDKCapture(id, data)
	var frame protocol.JSONObject
	if frame.UnmarshalJSON(data) != nil {
		return
	}
	if frame.String("type") == "result" {
		s.conversations.Finish(id, sourceJSONTruthy(frame.Get("is_error")))
	}
}
