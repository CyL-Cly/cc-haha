package desktopoauth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type OpenAISession struct {
	State, CodeVerifier, AuthorizeURL, RedirectURI string
	CreatedAt                                      int64
	server                                         *http.Server
	done                                           chan struct{}
	timer                                          *time.Timer
	ctx                                            context.Context
	cancel                                         context.CancelFunc
}

// One desktop-managed account owns its own callback listener and credential
// file. It uses the existing explicit filesystem/network dependencies, without
// changing the separate CLI credential slot or Claude desktop account.
type OpenAIService struct {
	parent       *Service
	callbackPort int
	startMu      sync.Mutex
	mu           sync.Mutex
	sessions     map[string]*OpenAISession
	owned        map[*OpenAISession]bool
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
	operations   sync.WaitGroup
	catalog      openAIModelCache
}

func NewOpenAIService(parent *Service, callbackPort *int) *OpenAIService {
	port := OpenAICodexOAuthPort
	if callbackPort != nil {
		port = *callbackPort
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &OpenAIService{parent: parent, callbackPort: port, sessions: map[string]*OpenAISession{}, owned: map[*OpenAISession]bool{}, ctx: ctx, cancel: cancel}
}

func (s *OpenAIService) beginOperation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, errors.New("OpenAI OAuth service is closed")
	}
	s.operations.Add(1)
	s.mu.Unlock()
	requestContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return requestContext, func() { stop(); cancel(); s.operations.Done() }, nil
}

func (s *OpenAIService) TokenPath() string {
	return filepath.Join(s.parent.options.ConfigDir, "cc-haha", "openai-oauth.json")
}

func (s *OpenAIService) LoadTokens(ctx context.Context) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.parent.options.Store.FileSystem().ReadFile(s.TokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(data))
	if err != nil {
		return nil, err
	}
	if string(normalized) == "null" {
		return nil, nil
	}
	return &Document{normalized}, nil
}

func (s *OpenAIService) SaveTokens(ctx context.Context, tokens protocol.JSONObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(encode(tokens))
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, normalized, "", "  "); err != nil {
		return err
	}
	fs := s.parent.options.Store.FileSystem()
	path := s.TokenPath()
	if err = fs.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(s.parent.options.PID) + "." + strconv.FormatInt(s.parent.options.Now().UnixMilli(), 10)
	renamed := false
	defer func() {
		if !renamed {
			_ = fs.Remove(tmp)
		}
	}()
	if err = fs.WriteFile(tmp, formatted.Bytes(), 0600); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = fs.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

func (s *OpenAIService) DeleteTokens(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.parent.options.Store.FileSystem().Remove(s.TokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *OpenAIService) StartSession(_ float64) (*OpenAISession, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, errors.New("OpenAI OAuth service is closed")
	}
	s.Dispose()
	verifierBytes, stateBytes := make([]byte, 64), make([]byte, 32)
	s.parent.mu.Lock()
	_, e1 := io.ReadFull(s.parent.options.Random, verifierBytes)
	_, e2 := io.ReadFull(s.parent.options.Random, stateBytes)
	s.parent.mu.Unlock()
	if e1 != nil {
		return nil, e1
	}
	if e2 != nil {
		return nil, e2
	}
	listener, err := net.Listen("tcp", "localhost:"+strconv.Itoa(s.callbackPort))
	if err != nil {
		return nil, fmt.Errorf("OpenAI OAuth callback port %d is unavailable: Failed to start OAuth callback server: %w", s.callbackPort, err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	session := &OpenAISession{State: hex.EncodeToString(stateBytes), CodeVerifier: hex.EncodeToString(verifierBytes), RedirectURI: "http://localhost:" + strconv.Itoa(port) + OpenAICodexRedirectPath, CreatedAt: s.parent.options.Now().UnixMilli(), done: make(chan struct{}), ctx: ctx, cancel: cancel}
	session.AuthorizeURL = BuildOpenAIAuthorizeURL(session.RedirectURI, session.CodeVerifier, session.State)
	session.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.callback(session, w, r) })}
	s.mu.Lock()
	s.sessions[session.State] = session
	s.owned[session] = true
	session.timer = time.AfterFunc(5*time.Minute, func() { s.closeSession(session) })
	s.mu.Unlock()
	go func() { defer close(session.done); _ = session.server.Serve(listener) }()
	return session, nil
}

func (s *OpenAIService) GetSession(state string) *OpenAISession     { return s.session(state, false) }
func (s *OpenAIService) ConsumeSession(state string) *OpenAISession { return s.session(state, true) }
func (s *OpenAIService) session(state string, consume bool) *OpenAISession {
	s.mu.Lock()
	value := s.sessions[state]
	if value != nil && s.parent.options.Now().UnixMilli()-value.CreatedAt > 5*60*1000 {
		delete(s.sessions, state)
		s.mu.Unlock()
		s.closeSession(value)
		return nil
	}
	if value != nil && consume {
		delete(s.sessions, state)
		value.timer.Stop()
	}
	s.mu.Unlock()
	return value
}

func (s *OpenAIService) CompleteSession(ctx context.Context, code, state string) (*Document, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	session := s.ConsumeSession(state)
	if session == nil {
		return nil, errors.New("OpenAI OAuth session not found or expired")
	}
	requestContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(session.ctx, cancel)
	defer func() { stop(); cancel() }()
	response, err := s.parent.exchangeOpenAICode(requestContext, code, session.RedirectURI, session.CodeVerifier)
	if err != nil {
		return nil, err
	}
	tokens, err := NormalizeOpenAITokens(response, s.parent.options.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	tokens = OpenAIStoredTokens(tokens)
	if err = s.SaveTokens(requestContext, tokens); err != nil {
		return nil, err
	}
	return &Document{encode(tokens)}, nil
}

func (s *OpenAIService) EnsureFreshTokens(ctx context.Context) (*Document, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	doc, err := s.LoadTokens(ctx)
	if err != nil || doc == nil {
		return doc, err
	}
	if !truthy(doc.Data) {
		return nil, nil
	}
	tokens := doc.Record()
	if string(tokens.Get("expiresAt")) == "null" || !OpenAITokenExpired(tokens.Get("expiresAt"), s.parent.options.Now().UnixMilli()) {
		return doc, nil
	}
	if !truthy(tokens.Get("refreshToken")) {
		return nil, nil
	}
	refreshed, err := s.parent.refreshOpenAITokens(ctx, tokens.String("refreshToken"))
	if err == nil {
		// The original desktop service explicitly builds the refresh input. Its
		// optional old fields are included only when truthy; passing the stored
		// document directly would retain false, zero and empty legacy values.
		existing := record()
		for _, name := range []string{"accessToken", "refreshToken", "expiresAt"} {
			existing.Set(name, tokens.Get(name))
		}
		for _, name := range []string{"idToken", "email", "accountId", "clientId"} {
			if truthy(tokens.Get(name)) {
				existing.Set(name, tokens.Get(name))
			}
		}
		updated := OpenAIStoredTokens(WithRefreshedOpenAIAccessToken(existing, refreshed, s.parent.options.Now().UnixMilli()))
		err = s.SaveTokens(ctx, updated)
		if err == nil {
			return &Document{encode(updated)}, nil
		}
	}
	if s.parent.options.OnRefreshFailure != nil {
		s.parent.options.OnRefreshFailure(expectedFailure(err), err)
	}
	return nil, nil
}

func (s *OpenAIService) callback(session *OpenAISession, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != OpenAICodexRedirectPath {
		w.WriteHeader(404)
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state != session.State {
		w.Header()["Content-Type"] = nil
		w.WriteHeader(400)
		if code == "" {
			_, _ = io.WriteString(w, "Authorization code not found")
		} else {
			_, _ = io.WriteString(w, "Invalid state parameter")
		}
		// Wait for this already-written response to finish before listener close.
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		go s.closeSession(session)
		return
	}
	_, err := s.CompleteSession(session.ctx, code, state)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	message := ""
	if err != nil {
		message = err.Error()
	}
	_, _ = io.WriteString(w, OpenAICallbackPage(err == nil, message, false))
	if flush, ok := w.(http.Flusher); ok {
		flush.Flush()
	}
	go s.closeSession(session)
}

func (s *OpenAIService) closeSession(session *OpenAISession) {
	s.mu.Lock()
	if s.sessions[session.State] == session {
		delete(s.sessions, session.State)
	}
	if session.timer != nil {
		session.timer.Stop()
	}
	session.cancel()
	_ = session.server.Close()
	delete(s.owned, session)
	s.mu.Unlock()
	<-session.done
}

func (s *OpenAIService) Dispose() {
	s.mu.Lock()
	sessions := make([]*OpenAISession, 0, len(s.owned))
	for session := range s.owned {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		s.closeSession(session)
	}
}

func (s *OpenAIService) Close() {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.Dispose()
	s.operations.Wait()
}
