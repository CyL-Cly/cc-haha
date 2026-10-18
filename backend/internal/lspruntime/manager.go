package lspruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	Owner              *cooperative.Owner
	Binding            *ownerasync.NativeBinding
	CWD                string
	PID                int
	Environment        map[string]string
	OnError            func(error)
	OnDebug            func(string)
	InteractiveCatalog bool
}
type ServerConfig struct {
	Name   string
	Config protocol.JSONObject
}
type server struct {
	mu                sync.Mutex
	name              string
	config            protocol.JSONObject
	state             string
	lastError         error
	client            *client
	crashes, restarts int
	generation        uint64
}
type Manager struct {
	mu          sync.Mutex
	options     Options
	servers     []*server
	opened      map[string]string
	ready       chan struct{}
	readyOnce   *sync.Once
	generation  uint64
	initError   error
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
	jobs        sync.WaitGroup
	diagnostics diagnosticRegistry
	children    map[*cooperative.Owner]*ownerasync.NativeBinding
}

func New(o Options) (*Manager, error) {
	if o.Owner == nil || o.Binding == nil || !o.Binding.Owns(o.Owner) || !filepath.IsAbs(o.CWD) || o.PID <= 0 {
		return nil, errors.New("LSP requires the actual Owner/native reference ledger and process paths")
	}
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	o.Environment = env
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{options: o, opened: map[string]string{}, ready: make(chan struct{}), readyOnce: &sync.Once{}, ctx: ctx, cancel: cancel}, nil
}
func (m *Manager) Owns(owner *cooperative.Owner, binding *ownerasync.NativeBinding) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && (m.options.Owner == owner && m.options.Binding.Same(binding) || m.children[owner].Same(binding))
}
func (m *Manager) Context() context.Context { return m.ctx }

// Enabled is existence of the actual manager, including pending/zero-server
// initialization. Passive Write/Edit never consult the tool's ENABLE_LSP_TOOL.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.initError == nil
}
func (m *Manager) Connected() bool {
	if !m.Enabled() {
		return false
	}
	m.mu.Lock()
	rows := append([]*server{}, m.servers...)
	m.mu.Unlock()
	for _, s := range rows {
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		if state != "error" {
			return true
		}
	}
	return false
}
func (m *Manager) Configure(configurations []ServerConfig, failure error) {
	m.mu.Lock()
	generation := m.generation
	m.mu.Unlock()
	m.ConfigureGeneration(generation, configurations, failure)
}
func (m *Manager) ConfigureGeneration(generation uint64, configurations []ServerConfig, failure error) {
	m.mu.Lock()
	if generation != m.generation {
		m.mu.Unlock()
		return
	}
	ready, once := m.ready, m.readyOnce
	if !m.closed {
		m.initError = failure
		if failure == nil {
			for _, row := range configurations {
				config := row.Config.Clone()
				var extensions protocol.JSONObject
				_ = extensions.UnmarshalJSON(config.Get("extensionToLanguage"))
				if config.String("command") == "" || len(extensions.OwnProperties()) == 0 || config.Get("restartOnCrash") != nil || config.Get("shutdownTimeout") != nil {
					continue
				}
				m.servers = append(m.servers, &server{name: row.Name, config: config, state: "stopped", generation: generation})
			}
		}
	}
	m.mu.Unlock()
	once.Do(func() { close(ready) })
}
func (m *Manager) Wait(ctx context.Context) error {
	m.mu.Lock()
	ready := m.ready
	m.mu.Unlock()
	select {
	case <-ready:
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.initError
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (m *Manager) serverFor(path string) *server {
	ext := strings.ToLower(filepath.Ext(path))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	for _, s := range m.servers {
		var extensions protocol.JSONObject
		_ = extensions.UnmarshalJSON(s.config.Get("extensionToLanguage"))
		if extensions.Get(ext) != nil {
			return s
		}
	}
	return nil
}
func URI(path string) string {
	absolute, _ := filepath.Abs(path)
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
}
func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func object(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
func (m *Manager) State() []protocol.JSONObject {
	m.mu.Lock()
	rows := append([]*server{}, m.servers...)
	m.mu.Unlock()
	out := []protocol.JSONObject{}
	for _, s := range rows {
		s.mu.Lock()
		row := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(s.name)}, protocol.JSONField{Name: "state", Value: raw(s.state)})
		if s.lastError != nil {
			row.Set("error", raw(s.lastError.Error()))
		}
		s.mu.Unlock()
		out = append(out, row)
	}
	return out
}

func (m *Manager) claimStart(s *server) (bool, error) {
	s.mu.Lock()
	if s.state == "running" || s.state == "starting" {
		s.mu.Unlock()
		return false, nil
	}
	max := 3
	if s.config.Get("maxRestarts") != nil {
		_ = json.Unmarshal(s.config.Get("maxRestarts"), &max)
	}
	if s.state == "error" && s.crashes > max {
		s.mu.Unlock()
		return false, fmt.Errorf("LSP server '%s' exceeded max crash recovery attempts (%d)", s.name, max)
	}
	s.state = "starting"
	s.mu.Unlock()
	return true, nil
}

func (m *Manager) start(s *server) error {
	claimed, err := m.claimStart(s)
	if err != nil || !claimed {
		return err
	}
	return m.startClaimed(s)
}
func (m *Manager) startClaimed(s *server) error {
	fail := func(err error) error {
		s.mu.Lock()
		s.state = "error"
		s.lastError = err
		peer := s.client
		s.mu.Unlock()
		if peer != nil {
			peer.abort()
		}
		return err
	}
	if err := m.ctx.Err(); err != nil {
		return fail(err)
	}
	cwd := s.config.String("workspaceFolder")
	if cwd == "" {
		cwd = m.options.CWD
	}
	m.mu.Lock()
	env := SubprocessEnvironment(m.options.Environment)
	m.mu.Unlock()
	for _, p := range object(s.config.Get("env")).OwnProperties() {
		var key, value string
		_ = json.Unmarshal(p.NameJSON, &key)
		_ = json.Unmarshal(p.Value, &value)
		env[key] = value
	}
	var args []string
	_ = json.Unmarshal(s.config.Get("args"), &args)
	peer, err := startClient(s.config.String("command"), args, cwd, env, func(method string, params json.RawMessage) {
		if method == "textDocument/publishDiagnostics" {
			m.diagnostics.register(s.name, params)
		}
	}, func(err error) { s.mu.Lock(); s.state = "error"; s.lastError = err; s.crashes++; s.mu.Unlock() }, func(method string, err error) {
		if m.options.OnError != nil {
			m.options.OnError(fmt.Errorf("LSP server %s notification %s failed: %s", s.name, method, err))
		}
		if m.options.OnDebug != nil {
			m.options.OnDebug("Notification " + method + " failed but continuing")
		}
	}, func(message string) {
		if m.options.OnDebug != nil {
			m.options.OnDebug("[LSP SERVER " + s.name + "] " + message)
		}
	})
	if err != nil {
		return fail(err)
	}
	s.mu.Lock()
	s.client = peer
	s.mu.Unlock()
	if err = m.ctx.Err(); err != nil {
		peer.abort()
		return fail(err)
	}
	ctx := m.ctx
	cancel := func() {}
	if timeout := s.config.Get("startupTimeout"); timeout != nil {
		var ms float64
		_ = json.Unmarshal(timeout, &ms)
		ctx, cancel = context.WithTimeout(ctx, time.Duration(ms*float64(time.Millisecond)))
	}
	defer cancel()
	_, err = peer.request(ctx, "initialize", InitializeParams(m.options.PID, cwd, s.config.Get("initializationOptions")))
	if err != nil {
		return fail(err)
	}
	if err = peer.notification("initialized", map[string]any{}); err != nil {
		return fail(err)
	}
	peer.mu.Lock()
	peer.initialized = true
	peer.mu.Unlock()
	s.mu.Lock()
	s.state = "running"
	s.crashes = 0
	s.lastError = nil
	s.mu.Unlock()
	return nil
}
func (m *Manager) running(path string) (*server, error) {
	if err := m.Wait(m.ctx); err != nil {
		return nil, err
	}
	s := m.serverFor(path)
	if s == nil {
		return nil, nil
	}
	if err := m.start(s); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *server) healthy() (*client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	initialized := false
	if s.client != nil {
		s.client.mu.Lock()
		initialized = s.client.initialized
		s.client.mu.Unlock()
	}
	if s.state != "running" || s.client == nil || !initialized {
		suffix := ""
		if s.lastError != nil {
			suffix = ", last error: " + s.lastError.Error()
		}
		return nil, fmt.Errorf("Cannot send request to LSP server '%s': server is %s%s", s.name, s.state, suffix)
	}
	return s.client, nil
}
func (m *Manager) Request(path, method string, params any) (json.RawMessage, bool, error) {
	s, err := m.running(path)
	if err != nil || s == nil {
		return nil, s != nil, err
	}
	peer, err := s.healthy()
	if err != nil {
		return nil, true, err
	}
	for attempt := 0; attempt <= 3; attempt++ {
		result, failure := peer.request(m.ctx, method, params)
		if failure == nil {
			return result, true, nil
		}
		var rpc *RPCError
		if !errors.As(failure, &rpc) || rpc.Code != -32801 || attempt == 3 {
			return nil, true, fmt.Errorf("LSP request '%s' failed for server '%s': %s", method, s.name, failure)
		}
		timer := time.NewTimer(500 * time.Millisecond * time.Duration(1<<attempt))
		select {
		case <-timer.C:
		case <-m.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, true, m.ctx.Err()
		}
	}
	return nil, true, nil
}
func (m *Manager) IsOpen(path string) bool {
	uri := URI(path)
	s := m.serverFor(path)
	if s == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opened[uri] == s.name
}
func (m *Manager) Open(path string, text json.RawMessage) error {
	s, err := m.running(path)
	if err != nil || s == nil {
		return err
	}
	uri := URI(path)
	m.mu.Lock()
	opened := m.opened[uri] == s.name
	m.mu.Unlock()
	if opened {
		return nil
	}
	peer, err := s.healthy()
	if err != nil {
		return err
	}
	language := object(s.config.Get("extensionToLanguage")).String(strings.ToLower(filepath.Ext(path)))
	if language == "" {
		language = "plaintext"
	}
	err = peer.notification("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": language, "version": 1, "text": text}})
	if err == nil {
		m.mu.Lock()
		if m.generation == s.generation {
			m.opened[uri] = s.name
		}
		m.mu.Unlock()
	}
	return err
}
func (m *Manager) Change(path string, text json.RawMessage) error {
	s := m.serverFor(path)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	running := s.state == "running"
	s.mu.Unlock()
	if !running || !m.IsOpen(path) {
		return m.Open(path, text)
	}
	peer, err := s.healthy()
	if err != nil {
		return err
	}
	return peer.notification("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": URI(path), "version": 1}, "contentChanges": []any{map[string]any{"text": text}}})
}
func (m *Manager) Save(path string) error {
	s := m.serverFor(path)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	running := s.state == "running"
	s.mu.Unlock()
	if !running {
		return nil
	}
	peer, err := s.healthy()
	if err != nil {
		return err
	}
	return peer.notification("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": URI(path)}})
}
func (m *Manager) CloseFile(path string) error {
	s := m.serverFor(path)
	if s == nil {
		return nil
	}
	peer, err := s.healthy()
	if err != nil {
		return err
	}
	if err = peer.notification("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": URI(path)}}); err == nil {
		m.mu.Lock()
		delete(m.opened, URI(path))
		m.mu.Unlock()
	}
	return err
}
func (m *Manager) NotifyChange(path string, text json.RawMessage) {
	if m == nil {
		return
	}
	m.diagnostics.clearDelivered("file://" + path)
	content := append(json.RawMessage(nil), text...)
	s := m.serverFor(path)
	if s == nil {
		return
	}
	s.mu.Lock()
	state, peer := s.state, s.client
	s.mu.Unlock()
	if state == "running" && peer != nil {
		if m.IsOpen(path) {
			// The original async functions synchronously enqueue these two
			// writes before their first await. Queue admission, not goroutine
			// scheduling, therefore preserves hot change-before-save order.
			change := peer.enqueueNotification("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": URI(path), "version": 1}, "contentChanges": []any{map[string]any{"text": content}}})
			save := peer.enqueueNotification("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": URI(path)}})
			m.notificationJob(path, "change", change)
			m.notificationJob(path, "save", save)
		} else {
			// openFile awaits ensureServerStarted, even for a running server.
			// The separately invoked save therefore queues before that open.
			save := peer.enqueueNotification("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": URI(path)}})
			m.notificationJob(path, "save", save)
			m.notificationJob(path, "change", func() error { return m.Open(path, content) })
		}
		return
	}
	claimed, err := m.claimStart(s)
	// Cold change invokes start synchronously up to its physical spawn await;
	// Source saveFile sees 'starting' and returns without a notification.
	m.notificationJob(path, "change", func() error {
		if err != nil {
			return err
		}
		if claimed {
			if err := m.startClaimed(s); err != nil {
				return err
			}
		}
		return m.Open(path, content)
	})
}
func (m *Manager) notificationJob(path, kind string, work func() error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.jobs.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.jobs.Done()
		if err := work(); err != nil {
			if m.options.OnDebug != nil {
				m.options.OnDebug(fmt.Sprintf("LSP: Failed to notify server of file %s for %s: %s", kind, path, err))
			}
			if m.options.OnError != nil {
				m.options.OnError(err)
			}
		}
	}()
}
func (m *Manager) BeginClose() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	m.cancel()
	rows := append([]*server{}, m.servers...)
	ready, once := m.ready, m.readyOnce
	m.mu.Unlock()
	once.Do(func() { close(ready) })
	for _, s := range rows {
		s.mu.Lock()
		peer, state := s.client, s.state
		s.mu.Unlock()
		if peer != nil {
			peer.mu.Lock()
			pending := len(peer.requests) > 0
			peer.mu.Unlock()
			peer.writeMu.Lock()
			writing := len(peer.writes) > 0
			peer.writeMu.Unlock()
			if pending || writing || state == "starting" {
				peer.abort()
			}
		}
	}
}
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.BeginClose()
	m.mu.Lock()
	rows := append([]*server{}, m.servers...)
	m.mu.Unlock()
	var failure error
	for _, s := range rows {
		s.mu.Lock()
		peer := s.client
		s.client = nil
		s.state = "stopping"
		s.mu.Unlock()
		if peer != nil {
			if err := peer.stop(ctx); err != nil && failure == nil {
				failure = err
			}
		}
		s.mu.Lock()
		s.state = "stopped"
		s.mu.Unlock()
	}
	m.jobs.Wait()
	return failure
}

// CatalogEnabled reflects the actual process REPL tool-pool sample. Detached
// children share this same parent pool; a headless initial pool predates LSP.
func (m *Manager) CatalogEnabled() bool {
	return m != nil && m.options.InteractiveCatalog && m.Connected()
}
func (m *Manager) ReportError(err error) {
	if err != nil && m.options.OnError != nil {
		m.options.OnError(err)
	}
}
