package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"golang.org/x/net/websocket"
)

// This fixture tests the transport boundary only. It is intentionally not
// evidence that a Go Agent/tool run succeeded; application E2E supplies the
// actual shared SessionFactory and mocks only the provider HTTP endpoint.
type transportSession struct {
	mu       sync.Mutex
	emit     Emit
	received chan json.RawMessage
	blocked  chan struct{}
	closed   atomic.Int32
	running  bool
}

func (session *transportSession) Handle(ctx context.Context, raw json.RawMessage) error {
	select {
	case session.received <- append(json.RawMessage(nil), raw...):
	case <-ctx.Done():
		return ctx.Err()
	}
	var frame struct{ Type string }
	_ = json.Unmarshal(raw, &frame)
	if frame.Type == "user_message" && session.blocked != nil {
		session.mu.Lock()
		session.running = true
		session.mu.Unlock()
		select {
		case <-session.blocked:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (session *transportSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	session.mu.Lock()
	running := session.running
	session.mu.Unlock()
	state := "idle"
	if running {
		state = "running"
	}
	frame, _ := json.Marshal(map[string]any{"type": "session_state", "turnState": state, "activeBackgroundTaskIds": []string{}})
	return []json.RawMessage{frame, json.RawMessage(`{"type":"permission_request","requestId":"pending","toolName":"Write","input":{"file_path":"fixture"},"future":{"preserved":true}}`)}, nil
}
func (session *transportSession) Close(context.Context) error { session.closed.Add(1); return nil }

func testOptions(t *testing.T, factory SessionFactory) Options {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(home, ".claude")
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	return Options{HomeDir: home, ConfigDir: config, WorkDir: work, Factory: factory}
}
func testHost(t *testing.T, options Options) (*Server, *httptest.Server) {
	t.Helper()
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(server)
	t.Cleanup(func() { _ = server.Close(context.Background()); host.Close() })
	return server, host
}
func requestJSON(t *testing.T, host *httptest.Server, method, path, body string, expected int) json.RawMessage {
	t.Helper()
	request, err := http.NewRequest(method, host.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expected {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, expected, raw)
	}
	return raw
}
func createTestSession(t *testing.T, host *httptest.Server, work string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"workDir": work, "permissionMode": "default"})
	raw := requestJSON(t, host, "POST", "/api/sessions", string(body), 201)
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.SessionID == "" {
		t.Fatal(string(raw))
	}
	return result.SessionID
}
func dial(t *testing.T, host *httptest.Server, id string) *websocket.Conn {
	t.Helper()
	socket, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	_ = socket.SetDeadline(time.Now().Add(3 * time.Second))
	return socket
}
func receive(t *testing.T, socket *websocket.Conn) json.RawMessage {
	t.Helper()
	var raw string
	if err := websocket.Message.Receive(socket, &raw); err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(raw)
}
func receiveType(t *testing.T, socket *websocket.Conn, want string) json.RawMessage {
	t.Helper()
	for count := 0; count < 10; count++ {
		raw := receive(t, socket)
		var frame struct{ Type string }
		_ = json.Unmarshal(raw, &frame)
		if frame.Type == want {
			return raw
		}
	}
	t.Fatalf("frame %s not received", want)
	return nil
}

func TestHTTPCreatesSourceTranscriptAndPassesOriginalWSControlBytes(t *testing.T) {
	var factoryCount atomic.Int32
	fake := &transportSession{received: make(chan json.RawMessage, 4), blocked: make(chan struct{})}
	options := testOptions(t, func(ctx context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
		factoryCount.Add(1)
		fake.emit = emit
		return fake, nil
	})
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	path := filepath.Join(options.ConfigDir, "projects", projectpath.Sanitize(options.WorkDir), id+".jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := bytes.Split(bytes.TrimSpace(before), []byte("\n"))
	if len(rows) != 2 {
		t.Fatalf("initial rows %d", len(rows))
	}
	var first, meta map[string]any
	_ = json.Unmarshal(rows[0], &first)
	_ = json.Unmarshal(rows[1], &meta)
	if first["type"] != "file-history-snapshot" || meta["type"] != "session-meta" || meta["workDir"] != options.WorkDir {
		t.Fatalf("initial transcript %s", before)
	}
	socket := dial(t, host, id)
	receiveType(t, socket, "connected")
	receiveType(t, socket, "permission_requests_snapshot")
	user := `{ "type":"user_message", "content":"write fixture", "attachments":[{"type":"file","path":"a"}],"future":{"bytes":9007199254740993} }`
	if err := websocket.Message.Send(socket, user); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-fake.received:
		if string(raw) != user {
			t.Fatalf("user frame altered: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("user was not forwarded")
	}
	permission := `{"type":"permission_response","requestId":"p1","allowed":true,"updatedInput":{"file_path":"b"},"permissionUpdates":[{"scope":"session"}],"runtimeOverride":{"providerId":null,"modelId":"fixture-model","effortLevel":"high"},"future":"retain"}`
	if err := websocket.Message.Send(socket, permission); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-fake.received:
		if string(raw) != permission {
			t.Fatalf("permission frame altered: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("pending user turn blocked permission response")
	}
	output := json.RawMessage(`{"type":"content_delta","text":"tool completed","future":{"source":"shared-agent"}}`)
	fake.emit(output)
	if raw := receiveType(t, socket, "content_delta"); !bytes.Equal(raw, output) {
		t.Fatalf("server output altered %s", raw)
	}
	if factoryCount.Load() != 1 {
		t.Fatalf("duplicate runtime %d", factoryCount.Load())
	}
	close(fake.blocked)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("transport wrote fake agent output into transcript")
	}
}

func TestReconnectUsesSameRuntimeAndAuthoritativePermissionSnapshot(t *testing.T) {
	fake := &transportSession{received: make(chan json.RawMessage, 2)}
	var count atomic.Int32
	options := testOptions(t, func(_ context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
		count.Add(1)
		fake.emit = emit
		return fake, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	first := dial(t, host, id)
	receiveType(t, first, "permission_requests_snapshot")
	if err := websocket.Message.Send(first, `{"type":"prewarm_session"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.received:
	case <-time.After(time.Second):
		t.Fatal("prewarm not forwarded")
	}
	_ = first.Close()
	second := dial(t, host, id)
	receiveType(t, second, "connected")
	permission := receiveType(t, second, "permission_request")
	if !strings.Contains(string(permission), `"future":{"preserved":true}`) {
		t.Fatal(string(permission))
	}
	if err := websocket.Message.Send(second, `{"type":"ping"}`); err != nil {
		t.Fatal(err)
	}
	receiveType(t, second, "pong")
	if count.Load() != 1 {
		t.Fatalf("reconnect created second agent %d", count.Load())
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.closed.Load() != 1 {
		t.Fatalf("shared session close count %d", fake.closed.Load())
	}
}

func TestRestartLoadsLegacyHistoryAndRuntimeMetadataWithoutRewriting(t *testing.T) {
	opened := make(chan SessionOptions, 1)
	fake := &transportSession{received: make(chan json.RawMessage, 1)}
	options := testOptions(t, func(_ context.Context, options SessionOptions, _ Emit) (LiveSession, error) {
		opened <- options
		return fake, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	path := filepath.Join(options.ConfigDir, "projects", projectpath.Sanitize(options.WorkDir), id+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"session-meta","runtimeProviderId":null,"runtimeModelId":"fixture-model","effortLevel":"high","unknown":{"original":true}}` + "\n" + `{"type":"session-meta","permissionMode":"acceptEdits","future":7}` + "\n" + `{"type":"user","uuid":"old-user","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"previous request\n\n<session_references>\nRead the referenced session.\n[{\"sessionId\":\"related\"}]\n</session_references>"},"sessionReferences":[{"sessionId":"opaque-legacy"}]}` + "\n" + `{"type":"assistant","uuid":"old-assistant","parentUuid":"old-user","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","model":"fixture-model","content":[{"type":"text","text":"previous result"}],"usage":{"input_tokens":3,"output_tokens":4}}}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	before, _ := os.ReadFile(path)
	_ = server.Close(context.Background())
	host.Close()
	_, restarted := testHost(t, options)
	raw := requestJSON(t, restarted, "GET", "/api/sessions/"+id+"/messages", "", 200)
	if !bytes.Contains(raw, []byte("previous result")) || !bytes.Contains(raw, []byte("related")) {
		t.Fatalf("legacy history missing %s", raw)
	}
	socket := dial(t, restarted, id)
	receiveType(t, socket, "permission_requests_snapshot")
	_ = websocket.Message.Send(socket, `{"type":"prewarm_session"}`)
	select {
	case received := <-opened:
		if received.ID != id || received.WorkDir != options.WorkDir || received.PermissionMode != "acceptEdits" || !received.Resume {
			t.Fatalf("resume opts %+v", received)
		}
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(received.Metadata, &metadata)
		if string(metadata["runtimeProviderId"]) != "null" || string(metadata["runtimeModelId"]) != `"fixture-model"` || string(metadata["unknown"]) != `{"original":true}` || string(metadata["future"]) != "7" {
			t.Fatalf("metadata lost %s", received.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("resume factory not opened")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("history read/restart mutated original transcript")
	}
}

func TestAccessBoundaryHealthBypassAndLocalControlCredential(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return &transportSession{received: make(chan json.RawMessage, 1)}, nil
	})
	options.Authorization = access.Authorization{LocalToken: "fixture-process-token"}
	_, host := testHost(t, options)
	requestJSON(t, host, "GET", "/health", "", 200)
	requestJSON(t, host, "GET", "/api/h5-access", "", 403)
	request, _ := http.NewRequest("GET", host.URL+"/api/h5-access", nil)
	request.Header.Set("Authorization", "Bearer fixture-process-token")
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 501 {
		t.Fatalf("local credential bypassed missing real H5 service: %d", response.StatusCode)
	}
	request, _ = http.NewRequest("GET", host.URL+"/api/sessions", nil)
	request.Header.Set("Origin", "https://outside.example")
	response, err = host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("remote browser accepted while H5 disabled: %d", response.StatusCode)
	}
}

func TestMissingBusinessDomainsFailExplicitlyInsteadOfEmptySuccess(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return &transportSession{received: make(chan json.RawMessage, 1)}, nil
	})
	_, host := testHost(t, options)
	requestJSON(t, host, "GET", "/api/conversations", "", 400)
	for _, domain := range []string{"models", "effort", "scheduled-tasks", "search", "agents", "teams", "workflows", "haha-oauth", "haha-openai-oauth", "haha-grok-oauth", "adapters", "skills", "market", "mcp", "connectors", "plugins", "computer-use", "diagnostics", "doctor", "activity-stats", "open-targets", "memory", "voice", "traces"} {
		raw := requestJSON(t, host, "GET", "/api/"+domain, "", 501)
		if !bytes.Contains(raw, []byte("GO_MIGRATION_NOT_READY")) {
			t.Fatal(string(raw))
		}
	}
}
