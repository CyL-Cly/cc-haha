// Package mcpruntime owns actual MCP connections and their discovery caches.
// It shares the native client's transports and the application's credential
// provider; it performs no automatic interactive authentication.
package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpheaders"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/textsanitization"
)

type Options struct {
	PID                 int
	HeadersPolicy       func(context.Context) (mcpheaders.Options, error)
	SessionIngressToken func() string
	SDK                 func(context.Context, string, protocol.JSONObject) (*mcpclient.SDKControlBindings, error)
	CWD, Version        string
	Environment         map[string]string
	HTTPClient          *http.Client
	PrepareHTTP         func(context.Context, protocol.JSONObject, mcpclient.Config) (mcpclient.Config, *http.Client, error)
	Auth                func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error)
	Enabled             func(context.Context, string, protocol.JSONObject) (bool, error)
	Authenticate        func(context.Context, string, protocol.JSONObject, func(string) error) error
	ClearOAuth          func(context.Context, string, protocol.JSONObject) error
}
type Connection struct {
	Name, Status string
	Config       protocol.JSONObject
	Client       *mcpclient.Client
	Tools        []protocol.JSONObject
	Failure      error
	execution    *Connection
	entry        *pending
	runtime      *Service
}
type pending struct {
	sdk    bool
	done   chan struct{}
	cancel context.CancelFunc
	value  Connection
	err    error
	active bool
}
type Service struct {
	mu            sync.Mutex
	options       Options
	entries       map[string]*pending
	sdkAttempts   map[*pending]bool
	sdkCurrent    map[string]*pending
	closed        bool
	namespaceRoot *Service
	namespaces    map[string]*Service
	closeDone     chan struct{}
	closeErr      error
	toolCache     map[string]*toolDiscovery
	toolOrder     []string
	discoveries   map[*toolDiscovery]bool
}
type toolDiscovery struct {
	execution *Connection
	done      chan struct{}
	tools     []protocol.JSONObject
	failure   error
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.CWD) || o.Environment == nil || o.Auth == nil || o.Enabled == nil || o.Authenticate == nil || o.ClearOAuth == nil {
		return nil, errors.New("MCP runtime requires actual cwd/environment, shared credential provider, execution policy and OAuth bindings")
	}
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	o.Environment = env
	if o.SessionIngressToken == nil {
		o.SessionIngressToken = remoteio.NewIngressAuth(env).Token
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	return &Service{options: o, entries: map[string]*pending{}, sdkAttempts: map[*pending]bool{}, sdkCurrent: map[string]*pending{}, toolCache: map[string]*toolDiscovery{}, discoveries: map[*toolDiscovery]bool{}}, nil
}
func (s *Service) key(name string, config protocol.JSONObject) string {
	data, _ := json.Marshal([]any{s.options.CWD, name, config})
	return string(protocol.StringifyValidatedJavaScriptJSON(data))
}
func configFor(value protocol.JSONObject) (mcpclient.Config, error) {
	out := mcpclient.Config{AuthToken: value.String("authToken"), Type: value.String("type"), Command: value.String("command"), URL: value.String("url"), Env: map[string]string{}, Headers: map[string]string{}}
	if value.Get("args") != nil {
		if err := json.Unmarshal(value.Get("args"), &out.Args); err != nil {
			return out, err
		}
	}
	if value.Get("env") != nil {
		if err := json.Unmarshal(value.Get("env"), &out.Env); err != nil {
			return out, err
		}
	}
	if value.Get("headers") != nil {
		if err := json.Unmarshal(value.Get("headers"), &out.Headers); err != nil {
			return out, err
		}
	}
	return out, nil
}
func clone(c Connection) Connection {
	c.Config = c.Config.Clone()
	tools := make([]protocol.JSONObject, len(c.Tools))
	for i, tool := range c.Tools {
		tools[i] = tool.Clone()
	}
	c.Tools = tools
	return c
}
func (s *Service) Connect(ctx context.Context, name string, config protocol.JSONObject) (Connection, error) {
	return s.connectTransport(ctx, name, config, true)
}

// ConnectTransport performs the original UI status probe's handshake. Tool
// discovery belongs to the Harness catalog, not to this connectivity check.
func (s *Service) ConnectTransport(ctx context.Context, name string, config protocol.JSONObject) (Connection, error) {
	return s.connectTransport(ctx, name, config, false)
}

func (s *Service) connectTransport(ctx context.Context, name string, config protocol.JSONObject, discover bool) (Connection, error) {
	enabled, err := s.options.Enabled(ctx, name, config)
	if err != nil {
		return Connection{}, err
	}
	if !enabled {
		return Connection{Name: name, Status: "disabled", Config: config.Clone(), Tools: []protocol.JSONObject{}, runtime: s}, nil
	}
	key := s.key(name, config)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Connection{}, context.Canceled
	}
	entry := s.entries[key]
	if entry == nil {
		root, cancel := context.WithCancel(context.Background())
		entry = &pending{done: make(chan struct{}), cancel: cancel}
		s.entries[key] = entry
		go s.connect(root, key, name, config.Clone(), entry)
	}
	s.mu.Unlock()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return Connection{}, context.Cause(ctx)
	}
	if entry.err != nil {
		return Connection{}, entry.err
	}
	value := clone(entry.value)
	enabled, err = s.options.Enabled(ctx, name, config)
	if err != nil {
		return Connection{}, err
	}
	if !enabled {
		_ = s.Disconnect(context.Background(), name, config)
		value.Client = nil
		value.Tools = []protocol.JSONObject{}
		value.Status = "disabled"
	}
	if value.Status == "connected" && discover {
		return s.discover(ctx, key, value)
	}
	return value, nil
}
func (s *Service) connect(ctx context.Context, key, name string, config protocol.JSONObject, entry *pending) {
	value := Connection{Name: name, Config: config, Status: "failed", Tools: []protocol.JSONObject{}, entry: entry, runtime: s}
	var failure error
	parsed, err := configFor(config)
	if err == nil && (parsed.Type == "ws" || parsed.Type == "http" || parsed.Type == "sse") {
		policy := mcpheaders.Options{Environment: s.options.Environment, CWD: s.options.CWD, Run: mcpheaders.NativeRun}
		if s.options.HeadersPolicy != nil {
			policy, err = s.options.HeadersPolicy(ctx)
		}
		if err == nil {
			parsed.Headers = mcpheaders.Resolve(ctx, name, config, policy)
		}
	}
	httpClient := s.options.HTTPClient
	if err == nil && config.String("type") == "claudeai-proxy" && s.options.PrepareHTTP != nil {
		parsed, httpClient, err = s.options.PrepareHTTP(ctx, config.Clone(), parsed)
	}
	if err != nil {
		failure = err
	} else if parsed.Type == "sdk" && !entry.sdk {
		failure = errors.New("SDK servers should be handled in print.ts")
	} else {
		auth, err := s.options.Auth(ctx, name, config)
		if err != nil {
			failure = err
		} else {
			options := mcpclient.Options{SessionIngressToken: s.options.SessionIngressToken, Name: name, Version: s.options.Version, Environment: s.options.Environment, CWD: s.options.CWD, HTTPClient: httpClient, Auth: auth, OnClose: func(bool) { s.peerClosed(key, entry) }}
			if parsed.Type == "sdk" && s.options.SDK != nil {
				options.SDK, err = s.options.SDK(ctx, name, config.Clone())
			}
			var client *mcpclient.Client
			if err == nil {
				client, err = mcpclient.Connect(ctx, parsed, options)
			}
			if err != nil {
				var unauthorized *mcpclient.AuthError
				if errors.As(err, &unauthorized) {
					value.Status = "needs-auth"
				} else {
					failure = err
				}
			} else {
				value.Client, value.Status = client, "connected"
				if parsed.Type == "ws-ide" || parsed.Type == "sse-ide" {
					// Source issues this notification without awaiting an IDE reply.
					// The client owns the send and the transport's actual close path.
					_ = client.Notify(ctx, "ide_connected", protocol.NewJSONObject(protocol.JSONField{Name: "pid", Value: json.RawMessage(strconv.Itoa(s.options.PID))}))
				}
			}
		}
	}
	value.Failure = errors.Join(value.Failure, failure)
	// Native connect errors are failed connection snapshots, as Source's
	// memoized connectToServer result. Cancellation remains a rejected wait.
	failure = nil
	s.mu.Lock()
	if s.closed || (!entry.sdk && s.entries[key] != entry) || ctx.Err() != nil {
		s.mu.Unlock()
		if value.Client != nil {
			_ = value.Client.Close()
		}
		entry.value = Connection{Name: name, Config: config, Status: "failed", Tools: []protocol.JSONObject{}, runtime: s}
		entry.err = context.Canceled
	} else {
		entry.value, entry.err = value, failure
		entry.active = value.Client != nil
		s.mu.Unlock()
	}
	close(entry.done)
}

func (s *Service) discover(ctx context.Context, key string, value Connection) (Connection, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Connection{}, context.Canceled
	}
	entry := s.toolCache[key]
	if entry == nil {
		entry = &toolDiscovery{done: make(chan struct{}), tools: []protocol.JSONObject{}}
		if value.Config.String("type") == "sdk" {
			execution := clone(value)
			entry.execution = &execution
		}
		s.toolCache[key] = entry
		s.discoveries[entry] = true
		go func() {
			defer func() { s.mu.Lock(); delete(s.discoveries, entry); s.mu.Unlock() }()
			if value.Client.Capabilities().Get("tools") != nil {
				result, err := value.Client.ListTools(ctx, nil)
				if err != nil {
					entry.failure = err
				} else {
					// Source sanitizes peer metadata before constructing tools.
					data, err := textsanitization.Recursive(result.Get("tools"))
					if err != nil {
						entry.failure = err
					} else if err := json.Unmarshal(data, &entry.tools); err != nil {
						entry.failure = err
					}
					filtered := []protocol.JSONObject{}
					for _, tool := range entry.tools {
						qualified := mcpclient.ToolName(value.Name, tool.String("name"))
						if !strings.HasPrefix(qualified, "mcp__ide__") || qualified == "mcp__ide__executeCode" || qualified == "mcp__ide__getDiagnostics" {
							filtered = append(filtered, tool)
						}
					}
					entry.tools = filtered
				}
			}
			close(entry.done)
		}()
	}
	if at := slices.Index(s.toolOrder, key); at >= 0 {
		s.toolOrder = append(s.toolOrder[:at], s.toolOrder[at+1:]...)
	}
	s.toolOrder = append(s.toolOrder, key)
	// Match the original fetchToolsForClient's 20-entry LRU independently of
	// the connection cache; eviction leaves the actual peer connected.
	if len(s.toolOrder) > 20 {
		delete(s.toolCache, s.toolOrder[0])
		s.toolOrder = s.toolOrder[1:]
	}
	s.mu.Unlock()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return Connection{}, context.Cause(ctx)
	}
	value.Tools = make([]protocol.JSONObject, len(entry.tools))
	for i, tool := range entry.tools {
		value.Tools[i] = tool.Clone()
	}
	value.Failure = entry.failure
	value.execution = entry.execution
	return value, nil
}
func (s *Service) Probe(ctx context.Context, name string, config protocol.JSONObject) (string, int, error) {
	value, err := s.Connect(ctx, name, config)
	return value.Status, len(value.Tools), err
}

func (s *Service) ExecutionEnabled(ctx context.Context, name string, config protocol.JSONObject) (bool, error) {
	return s.options.Enabled(ctx, name, config)
}
func (s *Service) Disconnect(ctx context.Context, name string, config protocol.JSONObject) error {
	return s.disconnect(ctx, name, config, nil)
}

// CloseProbe may retire only its own cached attempt. A completed old status
// probe cannot detach a reconnect that replaced it while it was in flight.
func (s *Service) CloseProbe(ctx context.Context, connection Connection) error {
	if connection.entry == nil {
		return nil
	}
	if connection.entry.sdk {
		return s.closeSDKAttempt(ctx, connection)
	}
	return s.disconnect(ctx, connection.Name, connection.Config, connection.entry)
}
func (s *Service) disconnect(ctx context.Context, name string, config protocol.JSONObject, expected *pending) error {
	s.mu.Lock()
	key := s.key(name, config)
	entry := s.entries[key]
	if expected != nil && entry != expected {
		s.mu.Unlock()
		return nil
	}
	delete(s.entries, key)
	delete(s.toolCache, key)
	if at := slices.Index(s.toolOrder, key); at >= 0 {
		s.toolOrder = append(s.toolOrder[:at], s.toolOrder[at+1:]...)
	}
	s.mu.Unlock()
	if entry == nil {
		return nil
	}
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if entry.value.Client != nil {
		return entry.value.Client.Close()
	}
	return nil
}
func (s *Service) Authenticate(ctx context.Context, name string, config protocol.JSONObject, onURL func(string) error) error {
	return s.options.Authenticate(ctx, name, config, onURL)
}
func (s *Service) ClearOAuth(ctx context.Context, name string, config protocol.JSONObject) error {
	return s.options.ClearOAuth(ctx, name, config)
}
func (s *Service) Close(ctx context.Context) error { return s.closeSharedNamespaces(ctx) }

func (s *Service) peerClosed(key string, entry *pending) {
	s.mu.Lock()
	if entry.sdk {
		if !entry.active {
			s.mu.Unlock()
			return
		}
		entry.active = false
		if s.sdkCurrent[key] == entry {
			delete(s.sdkCurrent, key)
		}
		s.mu.Unlock()
		entry.cancel()
		return
	}
	if !entry.active || s.entries[key] != entry {
		s.mu.Unlock()
		return
	}
	delete(s.entries, key)
	delete(s.toolCache, key)
	if at := slices.Index(s.toolOrder, key); at >= 0 {
		s.toolOrder = append(s.toolOrder[:at], s.toolOrder[at+1:]...)
	}
	s.mu.Unlock()
	entry.cancel()
}
