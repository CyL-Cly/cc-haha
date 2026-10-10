package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	"golang.org/x/net/websocket"
)

func TestActualDesktopDingTalkLauncherSharedMainPrintPermissionWriteReadColdResumeAndClose(t *testing.T) {
	actualDingTalkMainprint(t, "")
}
func TestActualDingTalkOfficialDeepSeekSharedMainPrintLiveModel(t *testing.T) {
	key := os.Getenv("CC_HAHA_DINGTALK_LIVE_TOKEN")
	if key == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_DINGTALK_LIVE_GATE") != "1" {
		t.Fatal("live gate not authorized")
	}
	actualDingTalkMainprint(t, key)
}

type dingTalkFlowTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (d dingTalkFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.dingtalk.com" {
		return d.next.RoundTrip(req)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = d.target.Scheme, d.target.Host
	copy.URL = &u
	return d.next.RoundTrip(copy)
}
func dingTalkFlowObject(data json.RawMessage) protocol.JSONObject {
	o := protocol.NewJSONObject()
	_ = o.UnmarshalJSON(data)
	return o
}

func actualDingTalkMainprint(t *testing.T, liveKey string) {
	t.Helper()
	var fakeRequests atomic.Int32
	target := ""
	var catalogsMu sync.Mutex
	catalogs := [][]string{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var input struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(body, &input)
		names := []string{}
		for _, tool := range input.Tools {
			names = append(names, tool.Name)
		}
		catalogsMu.Lock()
		catalogs = append(catalogs, names)
		catalogsMu.Unlock()
		switch fakeRequests.Add(1) {
		case 1:
			response(w, "dingtalk-write", "Write", map[string]string{"file_path": target, "content": "DINGTALK_SHARED_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "dingtalk-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			response(w, "dingtalk-done", "", nil, "DINGTALK_SHARED_MAINPRINT_DONE")
		case 4:
			response(w, "dingtalk-resume-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			response(w, "dingtalk-resumed", "", nil, "DINGTALK_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("extra duplicate model request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Environment["ENABLE_TOOL_SEARCH"] = "false"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-dingtalk-server-token"
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	target = filepath.Join(o.WorkDir, "dingtalk-mainprint-file.txt")
	if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), []byte(`{"future":{"preserve":true},"dingtalk":{"futurePlatform":{"keep":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if liveKey != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
		provider := map[string]any{"id": "dingtalk-official-validation", "presetId": "deepseek", "name": "Private DingTalk official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveKey, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		if err := os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), raw(map[string]any{"schemaVersion": 6, "activeId": "dingtalk-official-validation", "providers": []any{provider}, "providerOrder": []string{"dingtalk-official-validation"}}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	startDesktop := func() (*server.Server, *httptest.Server) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		actual, err := NewDesktopServer(ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		}})
		if err != nil {
			listener.Close()
			t.Fatal(err)
		}
		httpServer := httptest.NewUnstartedServer(actual)
		_ = httpServer.Listener.Close()
		httpServer.Listener = listener
		httpServer.Start()
		return actual, httpServer
	}
	actual, desktop := startDesktop()
	defer func() { _ = actual.Close(context.Background()); desktop.Close() }()
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "dingtalk": map[string]any{"clientId": "private-id", "clientSecret": "private-secret", "allowedUsers": []string{"U1"}}}
	req, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(raw(patch)))
	req.Header.Set("Authorization", "Bearer private-dingtalk-server-token")
	req.Header.Set("Content-Type", "application/json")
	saved, err := desktop.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	savedBody, _ := io.ReadAll(saved.Body)
	saved.Body.Close()
	if saved.StatusCode != 200 || !bytes.Contains(savedBody, []byte(`"preserve":true`)) || !bytes.Contains(savedBody, []byte(`"keep":true`)) || bytes.Contains(savedBody, []byte("private-secret")) {
		t.Fatal("real desktop settings preserve/masking", saved.StatusCode)
	}
	updates := make(chan protocol.JSONObject, 16)
	var platformAddress string
	message := func(id, text string) protocol.JSONObject {
		data := map[string]any{"msgId": id, "conversationType": "1", "senderStaffId": "U1", "sessionWebhook": platformAddress + "/reply", "msgtype": "text", "text": map[string]string{"content": text}}
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("CALLBACK")}, protocol.JSONField{Name: "headers", Value: raw(map[string]string{"topic": imruntime.DingTalkRobotTopic, "messageId": id})}, protocol.JSONField{Name: "data", Value: raw(string(raw(data)))})
	}
	done, resumed := make(chan struct{}), make(chan struct{})
	var finishOnce, resumeOnce sync.Once
	var approvals, connections, acks, closes atomic.Int32
	var platformMu sync.Mutex
	notices, logs := []string{}, []string{}
	permissionID := regexp.MustCompile(`需要权限确认 \[([^\]]+)\]`)
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		connections.Add(1)
		defer func() { conn.Close(); closes.Add(1) }()
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				var value protocol.JSONObject
				if websocket.JSON.Receive(conn, &value) != nil {
					return
				}
				if dingTalkFlowObject(value.Get("headers")).String("messageId") != "" {
					acks.Add(1)
				}
			}
		}()
		for {
			select {
			case frame := <-updates:
				if err := websocket.JSON.Send(conn, frame); err != nil {
					return
				}
			case <-readDone:
				return
			}
		}
	}}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/stream" {
			ws.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var value protocol.JSONObject
		if err := json.NewDecoder(req.Body).Decode(&value); err != nil {
			return
		}
		switch req.URL.Path {
		case "/v1.0/gateway/connections/open":
			if value.String("clientId") != "private-id" || value.String("clientSecret") != "private-secret" {
				t.Error("actual raw credentials not consumed")
			}
			_, _ = w.Write(raw(map[string]string{"endpoint": "wss" + strings.TrimPrefix(platformAddress, "https") + "/stream", "ticket": "private-ticket"}))
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"private-access","expireIn":7200}`))
		case "/reply":
			text := dingTalkFlowObject(value.Get("markdown")).String("text")
			platformMu.Lock()
			notices = append(notices, text)
			platformMu.Unlock()
			if match := permissionID.FindStringSubmatch(text); len(match) > 1 {
				n := approvals.Add(1)
				updates <- message(fmt.Sprintf("approval-%d", n), "/allow "+match[1])
			}
			_, _ = w.Write([]byte(`{}`))
		case "/v1.0/card/instances", "/v1.0/card/instances/deliver", "/v1.0/card/streaming":
			params := dingTalkFlowObject(dingTalkFlowObject(value.Get("cardData")).Get("cardParamMap"))
			text := params.String("msgContent")
			_, _ = w.Write([]byte(`{}`))
			if req.URL.Path == "/v1.0/card/instances" && req.Method == "PUT" && params.String("flowStatus") == "3" {
				platformMu.Lock()
				notices = append(notices, text)
				platformMu.Unlock()
				if strings.Contains(text, "DINGTALK_SHARED_MAINPRINT_DONE") {
					finishOnce.Do(func() { close(done) })
				}
				if strings.Contains(text, "DINGTALK_SHARED_MAINPRINT_RESUMED") {
					resumeOnce.Do(func() { close(resumed) })
				}
			}
		default:
			t.Error("unexpected private platform request", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer peer.Close()
	platformAddress = peer.URL
	peerURL, _ := url.Parse(peer.URL)
	platformHTTP := &http.Client{Transport: dingTalkFlowTransport{peerURL, peer.Client().Transport}}
	startAdapter := func(httpServer *httptest.Server) (*imruntime.DingTalkRuntime, context.CancelFunc, <-chan error) {
		env := map[string]string{}
		for key, value := range o.Environment {
			env[key] = value
		}
		env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(httpServer.URL, "http")
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan *imruntime.DingTalkRuntime, 1)
		exited := make(chan error, 1)
		go func() {
			code, err := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--dingtalk"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard, StartDingTalk: func(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
				cfg := imruntime.LoadPlatformConfig("dingtalk", home, o.WorkDir, options.Environment, func(string) {})
				runtime, err := imruntime.NewDingTalkRuntime(ctx, imruntime.DingTalkRuntimeOptions{Config: cfg, APIOptions: imruntime.DingTalkAPIOptions{HTTP: platformHTTP}, ServerHTTP: httpServer.Client(), Log: func(message string) { platformMu.Lock(); logs = append(logs, message); platformMu.Unlock() }, StreamOptions: imruntime.DingTalkStreamOptions{Dial: func(ctx context.Context, address string, _ *tls.Config) (*imruntime.Socket, error) {
					return imruntime.DialSocket(ctx, address, peer.Client().Transport.(*http.Transport).TLSClientConfig)
				}}})
				if err != nil {
					return nil, err
				}
				if err = runtime.Start(); err != nil {
					runtime.Close()
					return nil, err
				}
				started <- runtime
				return runtime, nil
			}})
			if err == nil && code != 0 {
				err = fmt.Errorf("launcher code %d", code)
			}
			exited <- err
		}()
		select {
		case runtime := <-started:
			return runtime, cancel, exited
		case err := <-exited:
			cancel()
			t.Fatal("public launcher", err)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("adapter startup")
		}
		return nil, cancel, exited
	}
	waitTask := func(signal <-chan struct{}, exited <-chan error, label string) {
		timeout := 30 * time.Second
		if liveKey != "" {
			timeout = 150 * time.Second
		}
		select {
		case <-signal:
		case err := <-exited:
			t.Fatal(label, err)
		case <-time.After(timeout):
			platformMu.Lock()
			snapshot := append([]string{}, notices...)
			logSnapshot := append([]string{}, logs...)
			platformMu.Unlock()
			t.Fatal(label, fakeRequests.Load(), approvals.Load(), snapshot, logSnapshot)
		}
	}
	stopAdapter := func(cancel context.CancelFunc, exited <-chan error) {
		cancel()
		select {
		case err := <-exited:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("adapter Close/join")
		}
	}
	prompt := "Write the private artifact then Read and finish."
	if liveKey != "" {
		prompt = fmt.Sprintf("Use Write to create %s with exact content DINGTALK_SHARED_MAINPRINT_FILE followed by a newline. Use Read to verify it, then reply only DINGTALK_SHARED_MAINPRINT_DONE. Do not use Bash or delegate.", target)
	}
	first := message("first", prompt)
	updates <- first
	updates <- first
	runtime, cancel, exited := startAdapter(desktop)
	defer cancel()
	waitTask(done, exited, "first task")
	file, err := os.ReadFile(target)
	if err != nil || string(file) != "DINGTALK_SHARED_MAINPRINT_FILE\n" || approvals.Load() != 1 {
		t.Fatal("actual Write/Read/permission", err, approvals.Load())
	}
	binding := runtime.Store.Get("dingtalk:dm:U1")
	if binding == nil {
		t.Fatal("session store absent")
	}
	sid := binding.SessionID
	stopAdapter(cancel, exited)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := actual.Close(closeCtx); err != nil {
		closeCancel()
		t.Fatal(err)
	}
	closeCancel()
	desktop.Close()
	if liveKey == "" && fakeRequests.Load() != 3 {
		t.Fatal("duplicate model requests", fakeRequests.Load())
	}
	actual, desktop = startDesktop()
	resumePrompt := "Read the existing file and finish."
	if liveKey != "" {
		resumePrompt = "Read the same file from the previous turn without modifying it, verify its exact content, then reply only DINGTALK_SHARED_MAINPRINT_RESUMED. Do not use Bash or delegate."
	}
	updates <- message("resume", resumePrompt)
	restarted, restartCancel, restartExited := startAdapter(desktop)
	defer restartCancel()
	waitTask(resumed, restartExited, "cold same session task")
	nextBinding := restarted.Store.Get("dingtalk:dm:U1")
	if nextBinding == nil || nextBinding.SessionID != sid {
		t.Fatal("cold server+adapter changed SID")
	}
	stopAdapter(restartCancel, restartExited)
	closeCtx, closeCancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := actual.Close(closeCtx); err != nil {
		closeCancel()
		t.Fatal(err)
	}
	closeCancel()
	desktop.Close()
	if liveKey == "" && fakeRequests.Load() != 5 {
		t.Fatal("alternate Harness or duplicate model", fakeRequests.Load())
	}
	if liveKey != "" && fakeRequests.Load() != 0 {
		t.Fatal("official live used fake model")
	}
	deadline := time.Now().Add(time.Second)
	for closes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if connections.Load() != 2 || closes.Load() != 2 || acks.Load() < 4 {
		t.Fatal("actual physical platform Close/ACK", connections.Load(), closes.Load(), acks.Load())
	}
	canonical := []byte{}
	_ = filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == sid+".jsonl" {
			canonical, _ = os.ReadFile(path)
		}
		return nil
	})
	for _, marker := range []string{`"name":"Write"`, `"name":"Read"`, "DINGTALK_SHARED_MAINPRINT_DONE", "DINGTALK_SHARED_MAINPRINT_RESUMED"} {
		if !bytes.Contains(canonical, []byte(marker)) {
			t.Fatal("actual canonical missing", marker)
		}
	}
	if liveKey == "" {
		catalogsMu.Lock()
		snapshot := append([][]string{}, catalogs...)
		catalogsMu.Unlock()
		for _, names := range snapshot {
			for _, needed := range []string{"Agent", "Workflow", "Write", "Read"} {
				found := false
				for _, name := range names {
					found = found || name == needed
				}
				if !found {
					t.Fatal("ordinary default catalog filtered", needed)
				}
			}
		}
	}
	if liveKey != "" {
		if bytes.Contains(canonical, []byte(liveKey)) {
			t.Fatal("credential in canonical")
		}
		receiptRoot := os.Getenv("CC_HAHA_DINGTALK_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(receiptRoot) {
			t.Fatal("private live receipt root required")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop PUT + ordinary Go launcher + private DingTalk TLS Stream/Card protocol peer + same Go MainPrint official DeepSeek Write/Read + actual IM approval + cold server/adapter same SID + physical Close; no real DingTalk account/message claim", "sessionId": sid, "endpoint": "https://api.deepseek.com/anthropic", "model": "deepseek-flash[1m]", "actualTools": []string{"Write", "Read"}, "catalog": "ordinary default, no forced Tools list", "actualPermissionCallbacks": approvals.Load(), "fakeModelRequests": fakeRequests.Load(), "physicalConnections": connections.Load(), "physicalClosed": closes.Load(), "actualFileExact": true, "credentialInCanonical": false}
		trace, traceErr := os.ReadFile(filepath.Join(config, "cc-haha", "traces", sid+".jsonl"))
		receipt["rawTracePersisted"] = traceErr == nil && len(trace) > 0
		if traceErr == nil {
			if err := os.WriteFile(filepath.Join(receiptRoot, "trace-private.jsonl"), trace, 0600); err != nil {
				t.Fatal(err)
			}
		}
		for name, data := range map[string][]byte{"receipt.json": raw(receipt), "canonical.jsonl": canonical, "artifact.txt": file} {
			if err := os.WriteFile(filepath.Join(receiptRoot, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
