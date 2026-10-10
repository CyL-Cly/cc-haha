package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncollaboration"
)

type collaborationHost struct {
	service  *sessioncollaboration.Service
	ready    chan struct{}
	failure  error
	endpoint string
	tokens   map[string]string
	mu       sync.Mutex
	events   []collaborationEvent
	wake     chan struct{}
	done     chan struct{}
}
type collaborationEvent struct {
	id      string
	data    json.RawMessage
	barrier chan struct{}
}

func (s *Server) newCollaborationHost() error {
	h := &collaborationHost{ready: make(chan struct{}), tokens: map[string]string{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	s.collaboration = h
	service, e := sessioncollaboration.New(sessioncollaboration.Options{StatePath: filepath.Join(s.options.ConfigDir, "cc-haha", "session-collaboration", "state.json"), List: s.collaborationList, Read: s.collaborationRead, Exists: func(ctx context.Context, id string) (bool, error) {
		_, e := s.findSession(ctx, id)
		var own *apiError
		if errors.As(e, &own) && own.status == 404 {
			return false, nil
		}
		return e == nil, e
	}, Titles: s.collaborationTitles, Create: s.collaborationCreate, RuntimeState: s.collaborationRuntimeState,
		Start: func(ctx context.Context, id string, message protocol.JSONObject) error {
			return s.deliverCollaboration(ctx, id, message, true)
		},
		Enqueue: func(ctx context.Context, id string, message protocol.JSONObject) error {
			return s.deliverCollaboration(ctx, id, message, false)
		},
		Stop: s.stopCollaborationRuntime,
		BeginStop: func(ctx context.Context, id string) <-chan error {
			result := make(chan error, 1)
			// Start each original stop request before awaiting the group results.
			result <- s.stopCollaborationRuntime(ctx, id)
			close(result)
			return result
		},
	})
	if e != nil {
		return e
	}
	h.service = service
	go func() { h.failure = service.Recover(s.ctx); close(h.ready); s.collaborationEvents(h) }()
	return nil
}

func (s *Server) collaborationService(ctx context.Context) (*sessioncollaboration.Service, error) {
	h := s.collaboration
	select {
	case <-h.ready:
		return h.service, h.failure
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
func (s *Server) setCollaborationEndpoint(address net.Addr) {
	host, port, e := net.SplitHostPort(address.String())
	if e != nil {
		return
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	s.collaboration.mu.Lock()
	s.collaboration.endpoint = "http://" + net.JoinHostPort(host, port)
	s.collaboration.mu.Unlock()
}
func (s *Server) bindCollaborationSession(options *SessionOptions) (func(), error) {
	token, e := uuid()
	if e != nil {
		return nil, e
	}
	h := s.collaboration
	h.mu.Lock()
	h.tokens[options.ID] = token
	endpoint := h.endpoint
	h.mu.Unlock()
	s.resetSDKSession(options.ID)
	s.resetSlashCommands(options.ID)
	if endpoint != "" {
		options.Environment = map[string]string{"CC_HAHA_DESKTOP_SERVER_URL": endpoint, "CC_HAHA_SESSION_COLLABORATION_TOKEN": token, "CC_HAHA_SESSION_ID": options.ID}
	}
	options.OnSDK = func(data json.RawMessage) {
		s.observeCollaborationSDK(options.ID, data)
		if s.authorizeSessionCredential(options.ID, token) {
			s.captureSlashCommandsSDK(options.ID, data)
			s.observeConversationSDK(options.ID, data)
			s.observeTeamSDK(options.ID, data)
		}
	}
	return func() {
		h.mu.Lock()
		revoked := false
		if h.tokens[options.ID] == token {
			delete(h.tokens, options.ID)
			revoked = true
		}
		h.mu.Unlock()
		if revoked {
			s.revokeSDKSession(options.ID)
		}
	}, nil
}
func (s *Server) collaborationCaller(request *http.Request) string {
	if request.Method != http.MethodPost {
		return ""
	}
	for name := range request.Header {
		if strings.EqualFold(name, "origin") {
			return ""
		}
	}
	id, auth := strings.Join(request.Header.Values("X-Session-Id"), ", "), strings.Join(request.Header.Values("Authorization"), ", ")
	if id == "" || !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	if s.authorizeSessionCredential(id, auth[7:]) {
		return id
	}
	return ""
}
func collaborationAction(path string) string {
	for _, action := range []string{"list", "read", "create", "send", "wait"} {
		if path == "/api/session-collaboration/"+action {
			return action
		}
	}
	return ""
}
func (s *Server) collaborationHTTP(w http.ResponseWriter, r *http.Request, action string, parts []string) error {
	caller := ""
	if action != "" {
		caller = s.collaborationCaller(r)
		if caller == "" {
			writeJSON(w, 401, map[string]string{"error": "Invalid session credential"})
			return nil
		}
	}
	service, e := s.collaborationService(r.Context())
	if e != nil {
		return e
	}
	var result protocol.JSONObject
	if action != "" {
		if action == "list" || action == "read" || action == "wait" {
			r = r.WithContext(requestSignalContext(r))
		}
		result, e = service.HandleAction(r, action, caller)
	} else {
		var query json.RawMessage
		if value := r.URL.Query().Get("query"); value != "" {
			query = quotedCollaboration(value)
		}
		ctx := r.Context()
		if r.Method == http.MethodGet && len(parts) == 0 {
			ctx = requestSignalContext(r)
		}
		result, e = service.HandleUI(ctx, r.Method, parts, query)
	}
	if e != nil {
		if r.Context().Err() != nil {
			w.WriteHeader(499)
			return nil
		}
		return e
	}
	writeJSON(w, 200, result)
	return nil
}
func quotedCollaboration(value string) json.RawMessage { b, _ := json.Marshal(value); return b }
func collaborationObject(value any) protocol.JSONObject {
	b, _ := json.Marshal(value)
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(b)
	return out
}

func (s *Server) collaborationRuntimeState(id string) string {
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	if current == nil {
		return "idle"
	}
	current.mu.Lock()
	live := current.live
	current.mu.Unlock()
	if actual, ok := live.(CollaborationSession); ok {
		return actual.CollaborationRuntime().String("state")
	}
	return "idle"
}
func (s *Server) deliverCollaboration(ctx context.Context, id string, message protocol.JSONObject, start bool) error {
	s.collaboration.mu.Lock()
	endpoint := s.collaboration.endpoint
	s.collaboration.mu.Unlock()
	if endpoint == "" {
		return errors.New("Desktop session host is not running")
	}
	live, e := s.live(id)
	if e != nil {
		return e
	}
	actual, ok := live.(CollaborationSession)
	if !ok {
		return unsupported("shared session collaboration inbox")
	}
	canSend := func() bool {
		return s.collaboration.service.CanDeliver(message.String("id")) && s.collaborationRuntimeState(id) != "stopped"
	}
	ack, e := actual.EnqueueCollaboration(ctx, message, start, canSend)
	if e != nil {
		return e
	}
	if ack.String("status") != "queued" && ack.String("status") != "consumed" {
		return errors.New("CLI did not acknowledge the session message")
	}
	if ack.String("status") == "consumed" {
		if e = s.collaboration.service.OnMessageConsumed(ctx, message.String("id"), id); e != nil {
			return e
		}
		if start {
			return s.collaboration.service.OnSessionState(ctx, id, "idle", protocol.NewJSONObject(), "")
		}
	} else if !start && s.collaborationRuntimeState(id) == "idle" && canSend() {
		_, e = actual.EnqueueCollaboration(ctx, message, true, canSend)
	}
	return e
}
func (s *Server) stopCollaborationRuntime(ctx context.Context, id string) error {
	s.mu.Lock()
	current := s.runtimes[id]
	s.mu.Unlock()
	if current == nil {
		return nil
	}
	current.mu.Lock()
	live := current.live
	current.mu.Unlock()
	if live == nil {
		return nil
	}
	return live.Handle(ctx, json.RawMessage(`{"type":"stop_generation"}`))
}
func (s *Server) collaborationRead(ctx context.Context, id string, options protocol.JSONObject) (protocol.JSONObject, error) {
	info, e := s.findSession(ctx, id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/messages", nil)
	query := request.URL.Query()
	query.Set("limit", "100")
	if cursor := options.String("cursor"); cursor != "" {
		query.Set("cursor", cursor)
	}
	request.URL.RawQuery = query.Encode()
	result, e := s.messages(ctx, info, request)
	return collaborationObject(result), e
}
func (s *Server) collaborationCreate(ctx context.Context, caller string, input protocol.JSONObject) (protocol.JSONObject, error) {
	info, e := s.findSession(ctx, caller)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	workDir := input.String("workDir")
	if input.Get("workDir") == nil {
		workDir = info.WorkDir
	}
	if workDir == "" {
		return protocol.JSONObject{}, &sessioncollaboration.APIError{Status: 409, Code: "SESSION_WORKSPACE_UNAVAILABLE", Message: "The source session working directory is unavailable"}
	}
	repository := s.inspectRepository(ctx, workDir)
	if repository.State != "ok" && repository.State != "not_git_repo" {
		return protocol.JSONObject{}, &sessioncollaboration.APIError{Status: 400, Code: "SESSION_REPOSITORY_UNAVAILABLE", Message: "Cannot resolve the target repository (" + repository.State + "); no session was created"}
	}
	body := collaborationObject(map[string]any{"workDir": workDir, "permissionMode": info.PermissionMode})
	runtime := collaborationObject(map[string]any{"permissionMode": info.PermissionMode, "providerId": info.RuntimeProviderID, "model": info.RuntimeModelID, "effort": info.EffortLevel})
	s.mu.Lock()
	current := s.runtimes[caller]
	s.mu.Unlock()
	if current != nil {
		current.mu.Lock()
		live := current.live
		current.mu.Unlock()
		if actual, ok := live.(CollaborationSession); ok {
			runtime = actual.CollaborationRuntime()
		}
	}
	body.Set("permissionMode", runtime.Get("permissionMode"))
	if repository.State == "ok" {
		body.Set("repository", json.RawMessage(`{"worktree":true}`))
	}
	data, _ := body.MarshalJSON()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/api/sessions", bytes.NewReader(data))
	created, e := s.createSession(request)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	result := collaborationObject(created)
	meta := protocol.NewJSONObject()
	meta.Set("workDir", result.Get("workDir"))
	title := input.Get("title")
	if title == nil {
		units, _ := protocol.DecodeJavaScriptJSONString(input.Get("prompt"))
		title = protocol.QuoteJavaScriptUTF16(units[:min(80, len(units))])
	}
	meta.Set("customTitle", title)
	meta.Set("permissionMode", runtime.Get("permissionMode"))
	provider := input.Get("providerId")
	if provider == nil {
		provider = runtime.Get("providerId")
	}
	if provider == nil {
		provider = json.RawMessage("null")
	}
	meta.Set("runtimeProviderId", provider)
	model := input.Get("model")
	if model == nil {
		model = runtime.Get("model")
	}
	meta.Set("runtimeModelId", model)
	meta.Set("effortLevel", runtime.Get("effort"))
	data, _ = meta.MarshalJSON()
	e = s.updateLaunchMetadata(ctx, result.String("sessionId"), data)
	return result, e
}
