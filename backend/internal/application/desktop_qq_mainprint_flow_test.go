package application

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crypto/tls"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	"golang.org/x/net/websocket"
)

func TestActualDesktopConfigQQLauncherSharedMainPrintWriteReadPermissionResumeAndClose(t *testing.T) {
	actualQQMainprint(t, "")
}
func TestActualQQOfficialDeepSeekSharedMainPrintLiveModel(t *testing.T) {
	token := os.Getenv("CC_HAHA_QQ_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_QQ_LIVE_GATE") != "1" {
		t.Fatal("unowned live model gate")
	}
	actualQQMainprint(t, token)
}
func actualQQMainprint(t *testing.T, liveToken string) {
	var fakeRequests atomic.Int32
	target := ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch fakeRequests.Add(1) {
		case 1:
			response(w, "qq-write", "Write", map[string]string{"file_path": target, "content": "QQ_SHARED_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "qq-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			response(w, "qq-finish", "", nil, "QQ_SHARED_MAINPRINT_DONE")
		case 4:
			response(w, "qq-resume-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			response(w, "qq-resume-finish", "", nil, "QQ_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("extra/duplicate Harness request")
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = nil
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-qq-server-token"
	target = filepath.Join(o.WorkDir, "qq-mainprint-file.txt")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	if liveToken != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Arguments.Tools = nil
		o.Arguments.SystemPrompt = ""
		o.Environment["ENABLE_TOOL_SEARCH"] = "false"
		o.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
		provider := map[string]any{"id": "qq-official-validation", "presetId": "deepseek", "name": "Private QQ official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		index := map[string]any{"schemaVersion": 6, "activeId": "qq-official-validation", "providers": []any{provider}, "providerOrder": []string{"qq-official-validation"}}
		if err := os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), raw(index), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server, err := NewDesktopServer(ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	desktop := httptest.NewUnstartedServer(server)
	_ = desktop.Listener.Close()
	desktop.Listener = listener
	desktop.Start()
	defer desktop.Close()
	// QR-owned credential fields are intentionally read-only through PUT, as in
	// the original desktop. Begin with a private old successful-binding fixture.
	legacy := []byte(`{"future":{"preserve":true},"qq":{"appId":"private-qq-app","appSecret":"private-platform-token"}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "qq": map[string]any{"allowedUsers": []string{"U1"}}}
	request, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(raw(patch)))
	request.Header.Set("Authorization", "Bearer private-qq-server-token")
	request.Header.Set("Content-Type", "application/json")
	saved, err := desktop.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(saved.Body)
	saved.Body.Close()
	if saved.StatusCode != 200 || !bytes.Contains(body, []byte(`"preserve":true`)) || bytes.Contains(body, []byte("private-platform-token")) {
		t.Fatal("actual desktop config boundary failed", saved.StatusCode, string(body))
	}
	updates := make(chan protocol.JSONObject, 8)
	message := func(id, text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "op", Value: raw(0)}, protocol.JSONField{Name: "s", Value: raw(11)}, protocol.JSONField{Name: "t", Value: raw("C2C_MESSAGE_CREATE")}, protocol.JSONField{Name: "d", Value: raw(map[string]any{"author": map[string]string{"user_openid": "U1"}, "content": text, "id": id, "timestamp": "private"})})
	}
	prompt := "Write the requested private artifact, read it, then finish."
	if liveToken != "" {
		prompt = fmt.Sprintf("Use Write to create %s with exact content QQ_SHARED_MAINPRINT_FILE followed by a newline. Use Read to verify it, then respond only QQ_SHARED_MAINPRINT_DONE. Do not use Bash or delegate.", target)
	}
	first := message("first", prompt)
	updates <- first
	updates <- first
	done, resumed := make(chan struct{}), make(chan struct{})
	var finishOnce, resumeOnce sync.Once
	var approvals atomic.Int32
	var packetMu sync.Mutex
	packets := []protocol.JSONObject{}
	closed := atomic.Int32{}
	permissionID := regexp.MustCompile(`需要权限确认 \[([^\]]+)\]`)
	firstGatewayClosed := make(chan struct{})
	var gatewayConnections atomic.Int32
	wsPeer := httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		firstConnection := gatewayConnections.Add(1) == 1
		defer conn.Close()
		defer closed.Add(1)
		defer func() {
			if firstConnection {
				close(firstGatewayClosed)
			}
		}()
		websocket.Message.Send(conn, string(raw(map[string]any{"op": 10, "d": map[string]int{"heartbeat_interval": 60000}})))
		var identify protocol.JSONObject
		if websocket.JSON.Receive(conn, &identify) != nil {
			return
		}
		if identify.String("op") == "" && string(identify.Get("op")) != "2" {
			t.Error("actual IDENTIFY absent")
		}
		websocket.Message.Send(conn, string(raw(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]string{"session_id": "private-gateway-session"}})))
		disconnected := make(chan struct{})
		go func() {
			defer close(disconnected)
			for {
				var packet string
				if websocket.Message.Receive(conn, &packet) != nil {
					return
				}
			}
		}()
		for {
			select {
			case <-disconnected:
				return
			case m := <-updates:
				if websocket.Message.Send(conn, string(raw(m))) != nil {
					return
				}
			case <-conn.Request().Context().Done():
				return
			}
		}
	}})
	defer wsPeer.Close()
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var value protocol.JSONObject
		_ = value.UnmarshalJSON(data)
		logical := r.Header.Get("X-Fixture-Logical-URL")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case logical == imruntime.QQTokenURL:
			w.Write(raw(map[string]any{"access_token": "private-qq-token", "expires_in": 7200}))
		case strings.HasSuffix(logical, "/gateway"):
			w.Write(raw(map[string]string{"url": "ws" + strings.TrimPrefix(wsPeer.URL, "http")}))
		default:
			packetMu.Lock()
			packets = append(packets, value)
			packetMu.Unlock()
			text := value.String("content")
			if text == "" {
				text = value.String("content_raw")
			}
			if match := permissionID.FindStringSubmatch(text); len(match) > 1 {
				n := approvals.Add(1)
				updates <- message(fmt.Sprintf("approval-%d", n), "/allow "+match[1])
			}
			w.Write(raw(map[string]any{"id": "private-qq-response"}))
			if strings.TrimSpace(text) == "QQ_SHARED_MAINPRINT_DONE" {
				finishOnce.Do(func() { close(done) })
			}
			if strings.TrimSpace(text) == "QQ_SHARED_MAINPRINT_RESUMED" {
				resumeOnce.Do(func() { close(resumed) })
			}
		}
	}))
	defer platform.Close()
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runtime *imruntime.QQRuntime
	started := make(chan struct{})
	launcherDone := make(chan error, 1)
	options := func(cfg imruntime.Config) imruntime.QQRuntimeOptions {
		return imruntime.QQRuntimeOptions{Config: cfg, ServerHTTP: desktop.Client(), Log: func(message string) { t.Log(message) }, ClientOptions: imruntime.QQClientOptions{HTTP: &http.Client{Transport: qqFlowRoute{platform}}}, GatewayOptions: imruntime.QQGatewayOptions{Dial: func(ctx context.Context, address string, h http.Header, config *tls.Config) (imruntime.QQGatewaySocket, error) {
			return imruntime.DialQQGatewaySocket(ctx, address, h, config)
		}}}
	}
	go func() {
		code, e := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--qq"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard, StartQQ: func(ctx context.Context, opts adapterlauncher.Options) (adapterlauncher.Process, error) {
			cfg := imruntime.LoadPlatformConfig("qq", home, o.WorkDir, opts.Environment, func(string) {})
			var e error
			runtime, e = imruntime.NewQQRuntime(ctx, options(cfg))
			if e != nil {
				return nil, e
			}
			runtime.Start()
			close(started)
			return runtime, nil
		}})
		if e == nil && code != 0 {
			e = fmt.Errorf("launcher code%d", code)
		}
		launcherDone <- e
	}()
	select {
	case <-started:
	case e := <-launcherDone:
		t.Fatal("QQ launcher failed", e)
	case <-time.After(10 * time.Second):
		t.Fatal("QQ startup absent")
	}
	deadline := 30 * time.Second
	if liveToken != "" {
		deadline = 150 * time.Second
	}
	select {
	case <-done:
	case e := <-launcherDone:
		t.Fatal("QQ runtime exited", e)
	case <-time.After(deadline):
		packetMu.Lock()
		copy := append([]protocol.JSONObject{}, packets...)
		packetMu.Unlock()
		t.Fatal("QQ full Harness reply missing", fakeRequests.Load(), approvals.Load(), string(raw(copy)))
	}
	data, e := os.ReadFile(target)
	if e != nil || string(data) != "QQ_SHARED_MAINPRINT_FILE\n" || approvals.Load() < 1 {
		t.Fatal("actual QQ file/approval failed", e, approvals.Load())
	}
	if liveToken == "" && fakeRequests.Load() != 3 {
		t.Fatal("duplicate/alternate model requests", fakeRequests.Load())
	}
	if liveToken != "" && fakeRequests.Load() != 0 {
		t.Fatal("live used model fixture")
	}
	stored := runtime.Store.Get("U1")
	if stored == nil {
		t.Fatal("same session binding absent")
	}
	exists, e := runtime.ServerClient.SessionExists(context.Background(), stored.SessionID)
	if e != nil || !exists {
		t.Fatal("actual same server session absent", e)
	}
	cancel()
	select {
	case e := <-launcherDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("launcher close not joined")
	}
	// Local Close joins the adapter; the remote loopback peer owns a separate
	// goroutine. Wait for it to retire before handing the next gateway a message,
	// so the old peer cannot destructively consume this fixture's shared queue.
	select {
	case <-firstGatewayClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("closed QQ gateway peer did not retire")
	}
	{
		restartCtx, restartCancel := context.WithCancel(context.Background())
		defer restartCancel()
		cfg := imruntime.LoadPlatformConfig("qq", home, o.WorkDir, env, func(string) {})
		restarted, e := imruntime.NewQQRuntime(restartCtx, options(cfg))
		if e != nil {
			t.Fatal(e)
		}
		defer restarted.Close()
		resumePrompt := "Read the existing file, then finish."
		if liveToken != "" {
			resumePrompt = fmt.Sprintf("Continue the same session. Use Read to verify %s still contains QQ_SHARED_MAINPRINT_FILE, then respond only QQ_SHARED_MAINPRINT_RESUMED. Do not use Bash or delegate.", target)
		}
		updates <- message("resume", resumePrompt)
		restarted.Start()
		select {
		case <-resumed:
		case <-time.After(deadline):
			packetMu.Lock()
			copy := append([]protocol.JSONObject{}, packets...)
			packetMu.Unlock()
			t.Fatal("QQ cold resume missing", "requests", fakeRequests.Load(), "gatewayPeerCloseCount", closed.Load(), "session", restarted.Store.Get("U1"), "bridgeOpen", restarted.Bridge.IsOpen("U1", stored.SessionID), "outbound", string(raw(copy)))
		}
		binding := restarted.Store.Get("U1")
		if binding == nil || binding.SessionID != stored.SessionID || (liveToken == "" && fakeRequests.Load() != 5) || (liveToken != "" && fakeRequests.Load() != 0) {
			t.Fatal("cold restart changed session/Harness", fakeRequests.Load())
		}
		restartCancel()
		restarted.Close()
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	if e = server.Close(closeCtx); e != nil {
		t.Fatal(e)
	}
	if liveToken != "" {
		canonical := []byte{}
		filepath.WalkDir(filepath.Join(config, "projects"), func(p string, entry os.DirEntry, e error) error {
			if e == nil && !entry.IsDir() && entry.Name() == stored.SessionID+".jsonl" {
				canonical, _ = os.ReadFile(p)
			}
			return nil
		})
		if bytes.Contains(canonical, []byte(liveToken)) {
			t.Fatal("credential in canonical")
		}
		for _, marker := range []string{`"name":"Write"`, `"name":"Read"`, "QQ_SHARED_MAINPRINT_DONE", "QQ_SHARED_MAINPRINT_RESUMED"} {
			if !bytes.Contains(canonical, []byte(marker)) {
				t.Fatal("live canonical missing", marker)
			}
		}
		root := os.Getenv("CC_HAHA_QQ_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(root) {
			t.Fatal("private receipt root required")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop PUT QQ config + public launcher + private QQ token/API/gateway + official DeepSeek shared MainPrint Write/Read/default catalogue + IM approvals + adapter cold restart same session Read + Close; no real QQ account/message claim", "model": "deepseek-flash[1m]", "sessionId": stored.SessionID, "actualPermissionCallbacks": approvals.Load(), "fakeModelRequests": fakeRequests.Load(), "actualFileExact": true, "sameSessionRestart": true, "gatewayPeerCloseCount": closed.Load(), "catalogue": "ordinary default, no forced Tools list"}
		for name, value := range map[string][]byte{"receipt.json": raw(receipt), "artifact.txt": data, "canonical.jsonl": canonical} {
			if e = os.WriteFile(filepath.Join(root, name), value, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}

type qqFlowRoute struct{ peer *httptest.Server }

func (r qqFlowRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "bots.qq.com" && req.URL.Host != "api.sgroup.qq.com" {
		return nil, fmt.Errorf("unowned QQ platform HTTP target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return r.peer.Client().Transport.RoundTrip(copy)
}
