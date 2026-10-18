// Package ccrsessions owns the Claude Code Web Sessions API used by --remote
// and --teleport. It is independent of --sdk-url's worker/ingress protocol.
package ccrsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

const AuthenticationRequired = "Claude Code web sessions require authentication with a Claude.ai account. API key authentication is not sufficient. Please run /login to authenticate, or check your authentication status with /status."

type Credentials struct {
	Token, Organization, Subscription, APIKey string
	Scopes                                    []string
}
type Title struct{ Title, Branch string }
type Options struct {
	RemoteBackendEnabled                               bool
	BaseURL, CWD, ConfigDir, TempDir, SessionID, Model string
	Environment                                        map[string]string
	HTTP                                               *http.Client
	Credentials                                        func(context.Context, bool) (Credentials, error)
	Settings                                           func(context.Context) (protocol.JSONObject, error)
	GenerateTitle                                      func(context.Context, string) (Title, error)
	GenerateSessionTitle                               func(context.Context, json.RawMessage) (json.RawMessage, error)
	Run                                                func(context.Context, worktree.ProcessRequest) (worktree.ProcessResult, error)
	Wait                                               func(context.Context, time.Duration) error
	Report                                             func(error)
	PolicyPollInterval                                 time.Duration
}

func (s *Service) RemoteBackendEnabled() bool { return s.options.RemoteBackendEnabled }

type Service struct {
	options       Options
	life          context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	closed        bool
	workers       sync.WaitGroup
	policyOnce    sync.Once
	policyMu      sync.RWMutex
	policyAllowed bool
	policyError   error
	policyActive  bool
}

func New(o Options) (*Service, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.TempDir) || o.Credentials == nil || o.Settings == nil || o.GenerateTitle == nil {
		return nil, errors.New("CCR Sessions API requires actual OAuth, settings, title producer and absolute native paths")
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	if o.Run == nil {
		o.Run = worktree.NativeRun
	}
	if o.Wait == nil {
		o.Wait = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-timer.C:
				return nil
			}
		}
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	o.Environment = cloneEnvironment(o.Environment)
	life, cancel := context.WithCancel(context.Background())
	return &Service{options: o, life: life, cancel: cancel}, nil
}
func cloneEnvironment(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
	s.options.HTTP.CloseIdleConnections()
}
func (s *Service) operation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, context.Canceled
	}
	s.workers.Add(1)
	s.mu.Unlock()
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.life, cancel)
	return child, func() { stop(); cancel(); s.workers.Done() }, nil
}
func (s *Service) report(err error) {
	if err != nil && s.options.Report != nil {
		s.options.Report(err)
	}
}
func (s *Service) credentials(ctx context.Context, refresh bool) (Credentials, error) {
	value, err := s.options.Credentials(ctx, refresh)
	if err != nil {
		return value, err
	}
	if value.Token == "" {
		return value, errors.New(AuthenticationRequired)
	}
	if value.Organization == "" {
		return value, errors.New("Unable to get organization UUID")
	}
	return value, nil
}
func (s *Service) request(ctx context.Context, method, path string, body []byte, credentials Credentials, beta bool, timeout time.Duration, extra http.Header) (protocol.JSONObject, int, error) {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	defer done()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, s.options.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+credentials.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	if credentials.Organization != "" {
		request.Header.Set("x-organization-uuid", credentials.Organization)
	}
	if beta {
		request.Header.Set("anthropic-beta", "ccr-byoc-2025-07-29")
	}
	for key, values := range extra {
		request.Header.Del(key)
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := s.options.HTTP.Do(request)
	if err != nil {
		return protocol.JSONObject{}, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return protocol.JSONObject{}, response.StatusCode, err
	}
	var result protocol.JSONObject
	if len(data) != 0 && result.UnmarshalJSON(data) != nil && response.StatusCode != 304 {
		return result, response.StatusCode, errors.New("Invalid CCR Sessions API JSON response")
	}
	return result, response.StatusCode, nil
}
func (s *Service) FetchSession(ctx context.Context, sid string) (protocol.JSONObject, error) {
	credentials, err := s.credentials(ctx, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value, status, err := s.request(ctx, "GET", "/v1/sessions/"+sid, nil, credentials, true, 15*time.Second, nil)
	if err != nil {
		return value, err
	}
	if status == 200 {
		return value, nil
	}
	switch status {
	case 404:
		return value, fmt.Errorf("Session not found: %s", sid)
	case 401:
		return value, errors.New("Session expired. Please run /login to sign in again.")
	}
	failure, _ := value.Object("error")
	if message := failure.String("message"); message != "" {
		return value, errors.New(message)
	}
	return value, fmt.Errorf("Failed to fetch session: %d %s", status, http.StatusText(status))
}
func (s *Service) ListSessions(ctx context.Context) ([]protocol.JSONObject, error) {
	credentials, err := s.credentials(ctx, false)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		value, status, err := s.request(ctx, "GET", "/v1/sessions", nil, credentials, true, 0, nil)
		if err == nil && status == 200 {
			var rows []protocol.JSONObject
			if err = json.Unmarshal(value.Get("data"), &rows); err != nil {
				return nil, err
			}
			return rows, nil
		}
		if err == nil && status < 500 {
			return nil, fmt.Errorf("Failed to fetch code sessions: %s", http.StatusText(status))
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if attempt == 4 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("Failed to fetch code sessions: %s", http.StatusText(status))
		}
		if err := s.options.Wait(ctx, 2*time.Second*time.Duration(1<<attempt)); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("Failed to fetch code sessions")
}

func (s *Service) teleportEvents(ctx context.Context, sid string, credentials Credentials) ([]protocol.JSONObject, bool, error) {
	var rows []protocol.JSONObject
	cursor := ""
	for page := 0; page < 100; page++ {
		query := "?limit=1000"
		if page > 0 {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		body, status, err := s.request(ctx, "GET", "/v1/code/sessions/"+sid+"/teleport-events"+query, nil, credentials, false, 20*time.Second, nil)
		if ctx.Err() != nil {
			return nil, false, context.Cause(ctx)
		}
		if err != nil {
			return nil, false, nil
		}
		if status == 404 {
			return rows, page > 0, nil
		}
		if status == 401 {
			return nil, false, errors.New("Your session has expired. Please run /login to sign in again.")
		}
		var events []protocol.JSONObject
		if status != 200 || json.Unmarshal(body.Get("data"), &events) != nil {
			return nil, false, nil
		}
		for _, event := range events {
			if payload, ok := event.Object("payload"); ok {
				rows = append(rows, payload.Clone())
			}
		}
		if body.Get("next_cursor") == nil || string(body.Get("next_cursor")) == "null" {
			return rows, true, nil
		}
		cursor = body.String("next_cursor")
	}
	return rows, true, nil
}

func (s *Service) FetchTranscript(ctx context.Context, sid string) ([]protocol.JSONObject, error) {
	credentials, err := s.credentials(ctx, false)
	if err != nil {
		return nil, err
	}
	rows, found, err := s.teleportEvents(ctx, sid, credentials)
	if err != nil {
		return nil, err
	}
	if !found {
		query := ""
		if truthy(s.options.Environment["CLAUDE_AFTER_LAST_COMPACT"]) {
			query = "?after_last_compact=true"
		}
		body, status, err := s.request(ctx, "GET", "/v1/session_ingress/session/"+sid+query, nil, credentials, false, 20*time.Second, nil)
		if err != nil {
			return nil, errors.New("Failed to fetch session logs")
		}
		switch status {
		case 200:
			if json.Unmarshal(body.Get("loglines"), &rows) != nil {
				return nil, errors.New("Failed to fetch session logs")
			}
		case 404:
			rows = []protocol.JSONObject{}
		case 401:
			return nil, errors.New("Your session has expired. Please run /login to sign in again.")
		default:
			return nil, errors.New("Failed to fetch session logs")
		}
	}
	filtered := []protocol.JSONObject{}
	for _, row := range rows {
		kind := row.String("type")
		if string(row.Get("isSidechain")) != "true" && (kind == "user" || kind == "assistant" || kind == "attachment" || kind == "system") {
			filtered = append(filtered, row.Clone())
		}
	}
	return filtered, nil
}
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func jsonRows(data []byte, output any) error { return json.Unmarshal(data, output) }
