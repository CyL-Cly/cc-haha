package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parsed(s string) protocol.JSONObject { return nested(json.RawMessage(s)) }
func fixtureInitialize() protocol.JSONObject {
	return parsed(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true},"resources":{"subscribe":true},"prompts":{}},"serverInfo":{"name":"owned-loopback","version":"1.0.0"},"instructions":"use actual tools"}`)
}
func fixtureResult(m rpcMessage) rpcMessage {
	response := rpcMessage{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "initialize":
		response.Result = raw(fixtureInitialize())
	case "tools/list":
		response.Result = []byte(`{"tools":[{"name":"read.file","title":"Read","description":"<content> 🧠","inputSchema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false,"x-owned":42},"outputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"unknown":"strip"},"_meta":{"anthropic/searchHint":"  Read\n Files ","private":{"keep":1}},"unknown":"strip"}],"nextCursor":"page-2","unknown":{"keep":true}}`)
	case "tools/call":
		response.Result = []byte(`{"content":[{"type":"text","text":"done <ok>","unknown":"strip"},{"type":"resource","resource":{"uri":"file:///result","text":"real","unknown":"keep"}}],"structuredContent":{"value":7},"_meta":{"keep":"yes"},"unknown":"keep"}`)
	case "resources/list":
		response.Result = []byte(`{"resources":[{"name":"result","uri":"file:///result","mimeType":"text/plain","unknown":"strip"}],"nextCursor":"next-resource"}`)
	case "resources/templates/list":
		response.Result = []byte(`{"resourceTemplates":[{"name":"files","uriTemplate":"file:///{path}","unknown":"strip"}]}`)
	case "resources/read":
		response.Result = []byte(`{"contents":[{"uri":"file:///result","text":"actual text","unknown":"keep"}]}`)
	case "prompts/list":
		response.Result = []byte(`{"prompts":[{"name":"review","arguments":[{"name":"path","required":true,"unknown":"strip"}],"unknown":"strip"}]}`)
	case "prompts/get":
		response.Result = []byte(`{"messages":[{"role":"user","content":{"type":"text","text":"Review it","unknown":"strip"},"unknown":"strip"}]}`)
	case "fail":
		response.Error = &RPCError{Code: -32602, Message: "Bad arguments", Data: []byte(`{"field":"path"}`)}
	default:
		response.Result = []byte(`{}`)
	}
	return response
}

type fixtureServer struct {
	server         *httptest.Server
	mu             sync.Mutex
	wire           []map[string]any
	stream         chan rpcMessage
	legacy         bool
	getMode        string
	status         map[string]int
	reverseReplies chan rpcMessage
	cancelReceived chan struct{}
	cancelOnce     sync.Once
}

func newFixtureServer(t *testing.T, legacy bool) *fixtureServer {
	f := &fixtureServer{legacy: legacy, stream: make(chan rpcMessage, 32), status: map[string]int{}, getMode: "405", reverseReplies: make(chan rpcMessage, 8), cancelReceived: make(chan struct{})}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *fixtureServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if !f.legacy && f.getMode == "405" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if f.legacy {
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /messages?owned=1\n\n")
		} else {
			_, _ = fmt.Fprint(w, ": connected\n\n")
		}
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case message := <-f.stream:
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(message))
				w.(http.Flusher).Flush()
			}
		}
	}
	if r.Method == "DELETE" {
		f.mu.Lock()
		f.wire = append(f.wire, map[string]any{"delete": true, "session": r.Header.Get("Mcp-Session-Id")})
		f.mu.Unlock()
		w.WriteHeader(405)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(b, &decoded)
	f.mu.Lock()
	f.wire = append(f.wire, map[string]any{"message": decoded, "accept": r.Header.Get("Accept"), "protocol": r.Header.Get("Mcp-Protocol-Version"), "session": r.Header.Get("Mcp-Session-Id"), "auth": r.Header.Get("Authorization"), "custom": r.Header.Get("X-Owned"), "userAgent": r.Header.Get("User-Agent")})
	status := f.status[fmt.Sprint(decoded["method"])]
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, `{"error":{"code":-32001,"message":"Session not found"}}`)
		return
	}
	message, err := decodeMessage(b)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	if message.Method == "" {
		f.reverseReplies <- message
		w.WriteHeader(202)
		return
	}
	if len(message.ID) == 0 {
		if message.Method == "notifications/cancelled" {
			f.cancelOnce.Do(func() { close(f.cancelReceived) })
		}
		w.WriteHeader(202)
		return
	}
	if message.Method == "await-cancel" {
		select {
		case <-f.cancelReceived:
		case <-r.Context().Done():
			return
		}
	}
	if message.Method == "hang" {
		if f.legacy {
			w.WriteHeader(202)
			return
		}
		<-r.Context().Done()
		return
	}
	response := fixtureResult(message)
	if message.Method == "reverse" {
		requests := []rpcMessage{
			{JSONRPC: "2.0", ID: raw("server-root"), Method: "roots/list"},
			{JSONRPC: "2.0", ID: raw("server-ping"), Method: "ping"},
			{JSONRPC: "2.0", ID: raw("server-elicit"), Method: "elicitation/create", Params: parsed(`{"message":"provide value","requestedSchema":{"type":"object","properties":{}}}`)},
		}
		if f.legacy {
			for _, request := range requests {
				f.stream <- request
			}
			w.WriteHeader(202)
			go func() {
				for i := 0; i < 3; i++ {
					<-f.reverseReplies
				}
				f.stream <- response
			}()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, request := range requests {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(request))
		}
		w.(http.Flusher).Flush()
		for i := 0; i < 3; i++ {
			select {
			case <-f.reverseReplies:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(response))
		return
	}
	if f.legacy {
		f.stream <- response
		w.WriteHeader(202)
		return
	}
	w.Header().Set("Mcp-Session-Id", "session-owned")
	if message.Method == "tools/call" {
		w.Header().Set("Content-Type", "text/event-stream")
		meta := nested(message.Params.Get("_meta"))
		if token := meta.Get("progressToken"); token != nil {
			_, _ = fmt.Fprintf(w, "id: progress-1\ndata: %s\n\n", raw(rpcMessage{JSONRPC: "2.0", Method: "notifications/progress", Params: object(protocol.JSONField{Name: "progressToken", Value: token}, field("progress", 1), field("total", 2), field("message", "working"))}))
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw(response))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
func (f *fixtureServer) snapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]map[string]any(nil), f.wire...)
	return out
}
func (f *fixtureServer) config() Config {
	typ := "http"
	if f.legacy {
		typ = "sse"
	}
	return Config{Type: typ, URL: f.server.URL + "/mcp", Headers: map[string]string{"Authorization": "Bearer fake-owned", "X-Owned": "test"}}
}
func options(home string) Options {
	cwd, _ := filepath.EvalSymlinks(home)
	return Options{CWD: cwd, Environment: map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin"}, Version: "999.0.0-local", RequestTimeout: time.Second, HTTPTimeout: time.Second, ConnectTimeout: time.Second, ReconnectDelay: time.Millisecond}
}
func mustConnect(t *testing.T, config Config, o Options) *Client {
	t.Helper()
	c, err := Connect(context.Background(), config, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestHTTPAndSSEActualLifecycle(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := newFixtureServer(t, legacy)
			o := options(t.TempDir())
			c := mustConnect(t, f.config(), o)
			for _, method := range []string{"tools/list", "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "resources/subscribe", "resources/unsubscribe", "ping"} {
				result, err := c.method(context.Background(), method, object(field("uri", "file:///result"), field("name", "review")), RequestOptions{})
				if err != nil || !result.IsObject() {
					t.Fatalf("%s: %v", method, err)
				}
			}
			var progress []protocol.JSONObject
			result, err := c.CallTool(context.Background(), "read.file", object(field("path", "result")), object(field("kept", true)), RequestOptions{OnProgress: func(p protocol.JSONObject) { progress = append(progress, p) }})
			if err != nil || result.Get("structuredContent") == nil {
				t.Fatalf("call: %v %s", err, raw(result))
			}
			if !legacy && (len(progress) != 1 || progress[0].String("message") != "working") {
				t.Fatalf("progress: %s", raw(progress))
			}
			_, err = c.Request(context.Background(), "fail", protocol.JSONObject{}, RequestOptions{})
			var rpc *RPCError
			if !errors.As(err, &rpc) || rpc.Code != -32602 || string(rpc.Data) != `{"field":"path"}` {
				t.Fatalf("RPC error: %#v", err)
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			wire := f.snapshot()
			if len(wire) != 13 {
				t.Fatalf("wire count %d", len(wire))
			}
			for i, row := range wire {
				message := row["message"].(map[string]any)
				if row["accept"] != "application/json, text/event-stream" || row["auth"] != "Bearer fake-owned" || row["custom"] != "test" {
					t.Fatalf("headers row %d", i)
				}
				if i > 0 && row["protocol"] != LatestProtocolVersion {
					t.Fatalf("protocol row %d: %s", i, row["protocol"])
				}
				if !legacy && i > 0 && row["session"] != "session-owned" {
					t.Fatalf("session row %d", i)
				}
				if _, ok := message["delete"]; ok {
					t.Fatal("Close unexpectedly terminated server session")
				}
			}
		})
	}
}

func TestCancellationTimeoutAndCloseRejectPending(t *testing.T) {
	f := newFixtureServer(t, false)
	c := mustConnect(t, f.config(), options(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := c.Request(ctx, "hang", object(), RequestOptions{CancelReason: "contract cancel"})
		result <- err
	}()
	waitWire(t, f, "hang")
	cancel()
	var rpc *RPCError
	if err := <-result; !errors.As(err, &rpc) || rpc.Code != -32001 || rpc.Message != "contract cancel" {
		t.Fatalf("cancel: %#v", err)
	}
	waitWire(t, f, "notifications/cancelled")
	_, err := c.Request(context.Background(), "hang", object(), RequestOptions{Timeout: 20 * time.Millisecond})
	if !errors.As(err, &rpc) || rpc.Code != -32001 || rpc.Message != "Request timed out" {
		t.Fatalf("timeout: %#v", err)
	}
	go func() { _, err := c.Request(context.Background(), "hang", object(), RequestOptions{}); result <- err }()
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	err = <-result
	if !errors.As(err, &rpc) || rpc.Code != -32000 {
		t.Fatalf("close pending: %#v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("close lifetime")
	}
}
func waitWire(t *testing.T, f *fixtureServer, method string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, v := range f.snapshot() {
			if message, ok := v["message"].(map[string]any); ok && message["method"] == method {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("No %s request", method)
}

func TestExplicitSessionTerminationAndExpiredSession(t *testing.T) {
	f := newFixtureServer(t, false)
	c := mustConnect(t, f.config(), options(t.TempDir()))
	if err := c.TerminateSession(context.Background()); err != nil || c.SessionID() != "" {
		t.Fatalf("termination: %v %s", err, c.SessionID())
	}
	if f.snapshot()[2]["session"] != "session-owned" {
		t.Fatal("DELETE omitted session")
	}
	f.mu.Lock()
	f.status["expired"] = 404
	f.mu.Unlock()
	_, err := c.Request(context.Background(), "expired", object(), RequestOptions{})
	if err == nil {
		t.Fatal("expired session succeeded")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("expired session didn't close")
	}
	if IsSessionExpired(&HTTPError{Status: 404, Message: "missing page"}) {
		t.Fatal("generic 404 marked expired")
	}
}

func TestMCPProcessHelper(t *testing.T) {
	if os.Getenv("CC_HAHA_MCP_CHILD") != "1" {
		return
	}
	fmt.Fprint(os.Stderr, "owned-child-started\n")
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		m, err := decodeMessage(line)
		if err != nil {
			continue
		}
		if trace := os.Getenv("CC_HAHA_MCP_TRACE"); trace != "" {
			file, _ := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if file != nil {
				_, _ = file.Write(line)
				_ = file.Close()
			}
		}
		if len(m.ID) == 0 || m.Method == "hang" {
			continue
		}
		if m.Method == "env" {
			response := rpcMessage{JSONRPC: "2.0", ID: m.ID, Result: raw(object(field("home", os.Getenv("HOME")), field("fake", os.Getenv("FAKE_ACCOUNT")), field("secret", os.Getenv("MUST_NOT_INHERIT"))))}
			_, _ = os.Stdout.Write(append(raw(response), '\n'))
			continue
		}
		_, _ = os.Stdout.Write(append(raw(fixtureResult(m)), '\n'))
	}
}
func childConfig(t *testing.T, home string) Config {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Type: "stdio", Command: executable, Args: []string{"-test.run=^TestMCPProcessHelper$"}, Env: map[string]string{"CC_HAHA_MCP_CHILD": "1", "FAKE_ACCOUNT": "fake-account", "CC_HAHA_MCP_TRACE": filepath.Join(home, "wire.jsonl")}}
}
func TestActualStdioProcessAndIsolatedEnvironment(t *testing.T) {
	home := t.TempDir()
	c := mustConnect(t, childConfig(t, home), options(home))
	pid := c.PID()
	if pid == 0 {
		t.Fatal("missing actual process")
	}
	result, err := c.Request(context.Background(), "env", object(), RequestOptions{})
	if err != nil || result.String("home") != home || result.String("fake") != "fake-account" || result.String("secret") != "" {
		t.Fatalf("env: %v %s", err, raw(result))
	}
	_, err = c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Stderr(), "owned-child-started") {
		t.Fatal("stdio stderr unavailable")
	}
	if err = c.Close(); err != nil || c.PID() != 0 {
		t.Fatalf("close child: %v pid=%d", err, c.PID())
	}
	data, err := os.ReadFile(filepath.Join(home, "wire.jsonl"))
	if err != nil || !bytes.Contains(data, []byte("notifications/initialized")) {
		t.Fatalf("actual wire: %v", err)
	}
}

func TestConnectTimeoutAndUnsupportedTransport(t *testing.T) {
	home := t.TempDir()
	o := options(home)
	o.ConnectTimeout = 15 * time.Millisecond
	// The real helper is fast. Test a legacy stream which never
	// supplies its required endpoint, rather than relying on machine speed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	start := time.Now()
	_, err := Connect(context.Background(), Config{Type: "sse", URL: server.URL}, o)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("connect timeout: %v", err)
	}
	_, err = Connect(context.Background(), Config{Type: "sdk"}, o)
	if err == nil {
		t.Fatal("SDK transport was silently accepted")
	}
}

type contractRequest struct {
	Method      string              `json:"method"`
	Params      protocol.JSONObject `json:"params,omitzero"`
	Progress    bool                `json:"progress,omitempty"`
	Timeout     int                 `json:"timeout,omitempty"`
	CancelAfter int                 `json:"cancelAfter,omitempty"`
}

func contractRequests() []contractRequest {
	return []contractRequest{
		{Method: "tools/list"}, {Method: "tools/list", Params: object(field("cursor", "page-2"))},
		{Method: "tools/call", Params: object(field("name", "read.file"), field("arguments", object(field("path", "result"))), field("_meta", object(field("kept", true)))), Progress: true},
		{Method: "resources/list"}, {Method: "resources/templates/list"}, {Method: "resources/read", Params: object(field("uri", "file:///result"))},
		{Method: "prompts/list"}, {Method: "prompts/get", Params: object(field("name", "review"), field("arguments", object(field("path", "result"))))},
		{Method: "ping"}, {Method: "fail"},
	}
}
func runContract(t *testing.T, c *Client, requests []contractRequest) ([]any, []protocol.JSONObject) {
	var values []any
	progress := []protocol.JSONObject{}
	for _, r := range requests {
		opts := RequestOptions{Timeout: time.Second}
		if r.Timeout > 0 {
			opts.Timeout = time.Duration(r.Timeout) * time.Millisecond
		}
		ctx, cancel := context.WithCancel(context.Background())
		var timer *time.Timer
		if r.CancelAfter > 0 {
			opts.CancelReason = "contract cancel"
			timer = time.AfterFunc(time.Duration(r.CancelAfter)*time.Millisecond, cancel)
		}
		if r.Progress {
			opts.OnProgress = func(p protocol.JSONObject) { progress = append(progress, p) }
		}
		var result protocol.JSONObject
		var err error
		var cursor *string
		if r.Params.Get("cursor") != nil {
			value := r.Params.String("cursor")
			cursor = &value
		}
		switch r.Method {
		case "tools/list":
			result, err = c.ListTools(ctx, cursor)
		case "tools/call":
			result, err = c.CallTool(ctx, r.Params.String("name"), nested(r.Params.Get("arguments")), nested(r.Params.Get("_meta")), opts)
		case "resources/list":
			result, err = c.ListResources(ctx, cursor)
		case "resources/templates/list":
			result, err = c.ListResourceTemplates(ctx, cursor)
		case "resources/read":
			result, err = c.ReadResource(ctx, r.Params.String("uri"))
		case "prompts/list":
			result, err = c.ListPrompts(ctx, cursor)
		case "prompts/get":
			result, err = c.GetPrompt(ctx, r.Params.String("name"), nested(r.Params.Get("arguments")))
		default:
			result, err = c.method(ctx, r.Method, r.Params, opts)
		}
		if timer != nil {
			timer.Stop()
		}
		cancel()
		if err != nil {
			var rpc *RPCError
			var code any
			if errors.As(err, &rpc) {
				code = rpc.Code
			}
			values = append(values, map[string]any{"error": map[string]any{"code": code, "message": err.Error()}})
		} else {
			values = append(values, map[string]any{"result": result})
		}
	}
	return values, progress
}

func originalContract(t *testing.T, bun, home string, config Config, requests []contractRequest, environments ...map[string]string) map[string]any {
	t.Helper()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_CLIENT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "DISABLE_NON_ESSENTIAL_MODEL_CALLS=1"}
	for _, environment := range environments {
		for _, key := range []string{"CLAUDE_AGENT_SDK_VERSION", "CLAUDE_AGENT_SDK_CLIENT_APP"} {
			if value, ok := environment[key]; ok {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
	}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"config": map[string]any{"type": config.Type, "url": config.URL, "command": config.Command, "args": config.Args, "env": config.Env, "headers": config.Headers}, "requests": requests, "names": []string{}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	capturePublicGoCoverage(t, cmd)
	expected, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source failed: %v %s", err, bounded(stderr.String()))
	}
	var result map[string]any
	if err = json.Unmarshal(expected, &result); err != nil {
		t.Fatalf("actual Source JSON: %v %s", err, bounded(string(expected)))
	}
	return result
}

func TestSDKClientIdentityMatchesActualSourceHTTPWire(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	for _, environment := range []map[string]string{
		{"CLAUDE_AGENT_SDK_VERSION": "0.2.3"},
		{"CLAUDE_AGENT_SDK_CLIENT_APP": "private-desktop/1.0"},
		{"CLAUDE_AGENT_SDK_VERSION": "0.2.3", "CLAUDE_AGENT_SDK_CLIENT_APP": "private-desktop/1.0"},
		{"CLAUDE_AGENT_SDK_VERSION": "", "CLAUDE_AGENT_SDK_CLIENT_APP": ""},
	} {
		t.Run(fmt.Sprint(environment), func(t *testing.T) {
			home := t.TempDir()
			goPeer, sourcePeer := newFixtureServer(t, false), newFixtureServer(t, false)
			opts := options(home)
			for key, value := range environment {
				opts.Environment[key] = value
			}
			client := mustConnect(t, goPeer.config(), opts)
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			originalContract(t, bun, home, sourcePeer.config(), []contractRequest{}, environment)
			assertJSONEqual(t, raw(orderIndependentServerReplies(goPeer.snapshot())), raw(orderIndependentServerReplies(sourcePeer.snapshot())))
		})
	}
}
func TestRequestsAndResultsMatchActualOriginalClient(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	for _, transportType := range []string{"http", "sse", "stdio"} {
		t.Run(transportType, func(t *testing.T) {
			home := t.TempDir()
			_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
			var sourceConfig, goConfig Config
			var sourceWire, goWire func() any
			if transportType == "stdio" {
				goConfig = childConfig(t, home)
				sourceConfig = goConfig
				sourceConfig.Env = map[string]string{}
				for k, v := range goConfig.Env {
					sourceConfig.Env[k] = v
				}
				sourceConfig.Env["CC_HAHA_MCP_TRACE"] = filepath.Join(home, "source-wire.jsonl")
				goWire = func() any { return readJSONLines(t, filepath.Join(home, "wire.jsonl")) }
				sourceWire = func() any { return readJSONLines(t, filepath.Join(home, "source-wire.jsonl")) }
			} else {
				goServer := newFixtureServer(t, transportType == "sse")
				sourceServer := newFixtureServer(t, transportType == "sse")
				goConfig = goServer.config()
				sourceConfig = sourceServer.config()
				goWire = func() any { return goServer.snapshot() }
				sourceWire = func() any { return sourceServer.snapshot() }
			}
			names := []string{"local files", "name.with.dot", "🧠 read", "claude.ai Account 🧠", "__local__"}
			requests := contractRequests()
			if transportType != "stdio" {
				requests = append(requests, contractRequest{Method: "reverse"})
			}
			c := mustConnect(t, goConfig, options(home))
			values, progress := runContract(t, c, requests)
			var wireVersion any
			if transportType == "http" {
				wireVersion = c.ProtocolVersion()
			}
			actual := map[string]any{"values": values, "progress": progress, "metadata": map[string]any{"capabilities": c.Capabilities(), "serverInfo": c.ServerInfo(), "instructions": json.RawMessage(c.Instructions()), "protocolVersion": wireVersion}, "names": []string{}, "toolName": ToolName("claude.ai Account 🧠", "read.file")}
			resultJSON := []any{}
			for _, value := range values {
				result, ok := value.(map[string]any)["result"]
				if ok {
					resultJSON = append(resultJSON, string(raw(result)))
				} else {
					resultJSON = append(resultJSON, nil)
				}
			}
			actual["resultJSON"] = resultJSON
			normalized := []string{}
			for _, n := range names {
				normalized = append(normalized, NormalizeName(n))
			}
			actual["names"] = normalized
			_ = c.Close()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-client.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_CLIENT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "DISABLE_NON_ESSENTIAL_MODEL_CALLS=1"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"config": map[string]any{"type": sourceConfig.Type, "url": sourceConfig.URL, "command": sourceConfig.Command, "args": sourceConfig.Args, "env": sourceConfig.Env, "headers": sourceConfig.Headers}, "requests": requests, "names": names}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			capturePublicGoCoverage(t, cmd)
			expected, err := cmd.Output()
			if err != nil {
				t.Fatalf("actual Source failed: %v; %s", err, bounded(stderr.String()))
			}
			assertJSONEqual(t, raw(actual), expected)
			assertJSONEqual(t, raw(orderIndependentServerReplies(goWire())), raw(orderIndependentServerReplies(sourceWire())))
		})
	}
}
func orderIndependentServerReplies(wire any) any {
	rows, ok := wire.([]map[string]any)
	if !ok {
		return wire
	}
	requests := []map[string]any{}
	replies := []map[string]any{}
	for _, row := range rows {
		if message, ok := row["message"].(map[string]any); ok && message["method"] == nil {
			replies = append(replies, row)
		} else {
			requests = append(requests, row)
		}
	}
	sort.Slice(replies, func(i, j int) bool {
		return fmt.Sprint(replies[i]["message"].(map[string]any)["id"]) < fmt.Sprint(replies[j]["message"].(map[string]any)["id"])
	})
	return map[string]any{"requests": requests, "serverReplies": replies}
}
func readJSONLines(t *testing.T, path string) []any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := []any{}
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var v any
		if err = json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}
func bounded(s string) string {
	if len(s) > 1500 {
		return s[:1500] + "…"
	}
	return s
}
func assertJSONEqual(t *testing.T, actual, expected []byte) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(actual, &a); err != nil {
		t.Fatalf("Go JSON %v", err)
	}
	if err := json.Unmarshal(expected, &b); err != nil {
		t.Fatalf("Source JSON %v; %s", err, bounded(string(expected)))
	}
	if !reflect.DeepEqual(a, b) {
		if am, ok := a.(map[string]any); ok {
			if bm, ok := b.(map[string]any); ok {
				for key, av := range am {
					bv := bm[key]
					if reflect.DeepEqual(av, bv) {
						continue
					}
					if list, ok := av.([]any); ok {
						bl, _ := bv.([]any)
						for i, v := range list {
							if i >= len(bl) {
								t.Errorf("%s[%d] Source missing", key, i)
							} else if !reflect.DeepEqual(v, bl[i]) {
								t.Errorf("%s[%d] Go=%s Source=%s", key, i, bounded(string(raw(v))), bounded(string(raw(bl[i]))))
							}
						}
					} else {
						t.Errorf("%s Go=%s Source=%s", key, bounded(string(raw(av))), bounded(string(raw(bv))))
					}
				}
				return
			}
		}
		t.Errorf("Go=%s\nSource=%s", bounded(string(actual)), bounded(string(expected)))
	}
}
