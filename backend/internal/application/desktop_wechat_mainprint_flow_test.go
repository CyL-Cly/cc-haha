package application

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

func TestActualDesktopConfigWechatLauncherSharedMainPrintWriteReadPermissionResumeAndClose(t *testing.T) {
	actualWechatMainprint(t, "")
}
func TestActualWechatOfficialDeepSeekSharedMainPrintLiveModel(t *testing.T) {
	token := os.Getenv("CC_HAHA_WECHAT_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_WECHAT_LIVE_GATE") != "1" {
		t.Fatal("unowned live model gate")
	}
	actualWechatMainprint(t, token)
}
func actualWechatMainprint(t *testing.T, liveToken string) {
	var fakeRequests atomic.Int32
	target := ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch fakeRequests.Add(1) {
		case 1:
			response(w, "wechat-write", "Write", map[string]string{"file_path": target, "content": "WECHAT_SHARED_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "wechat-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			response(w, "wechat-finish", "", nil, "WECHAT_SHARED_MAINPRINT_DONE")
		case 4:
			response(w, "wechat-resume-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			response(w, "wechat-resume-finish", "", nil, "WECHAT_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("extra/duplicate Harness request")
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-wechat-server-token"
	target = filepath.Join(o.WorkDir, "wechat-mainprint-file.txt")
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
		provider := map[string]any{"id": "wechat-official-validation", "presetId": "deepseek", "name": "Private Wechat official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		index := map[string]any{"schemaVersion": 6, "activeId": "wechat-official-validation", "providers": []any{provider}, "providerOrder": []string{"wechat-official-validation"}}
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
	legacy := []byte(`{"future":{"preserve":true},"wechat":{"accountId":"private-wechat-account","botToken":"private-platform-token"}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "wechat": map[string]any{"allowedUsers": []string{"U1"}}}
	request, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(raw(patch)))
	request.Header.Set("Authorization", "Bearer private-wechat-server-token")
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
	updates := make(chan []any, 8)
	message := func(id, text string) any {
		return map[string]any{"from_user_id": "U1", "message_id": id, "context_token": "private-context", "item_list": []any{map[string]any{"type": 1, "text_item": map[string]string{"text": text}}}}
	}
	prompt := "Write the requested private artifact, read it, then finish."
	if liveToken != "" {
		prompt = fmt.Sprintf("Use Write to create %s with exact content WECHAT_SHARED_MAINPRINT_FILE followed by a newline. Use Read to verify the same file, then respond with only WECHAT_SHARED_MAINPRINT_DONE. Do not use Bash or delegate this task.", target)
	}
	first := message("first", prompt)
	updates <- []any{first, first}
	done, resumed := make(chan struct{}), make(chan struct{})
	var finishOnce, resumeOnce sync.Once
	var approvals atomic.Int32
	var platformMu sync.Mutex
	var peerPolls sync.WaitGroup
	notices := []string{}
	permissionID := regexp.MustCompile(`需要权限确认 \[([^\]]+)\]`)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var value protocol.JSONObject
		if err := json.NewDecoder(req.Body).Decode(&value); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/ilink/bot/getupdates":
			peerPolls.Add(1)
			defer peerPolls.Done()
			select {
			case batch := <-updates:
				_, _ = w.Write(raw(map[string]any{"ret": 0, "get_updates_buf": "private-cursor", "msgs": batch}))
			case <-req.Context().Done():
				return
			}
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/sendmessage":
			msg := protocol.NewJSONObject()
			_ = msg.UnmarshalJSON(value.Get("msg"))
			var items []protocol.JSONObject
			_ = json.Unmarshal(msg.Get("item_list"), &items)
			if len(items) != 1 {
				t.Error("actual reply has no text item")
				return
			}
			textObject := protocol.NewJSONObject()
			_ = textObject.UnmarshalJSON(items[0].Get("text_item"))
			text := textObject.String("text")
			platformMu.Lock()
			notices = append(notices, text)
			platformMu.Unlock()
			if match := permissionID.FindStringSubmatch(text); len(match) > 1 {
				n := approvals.Add(1)
				updates <- []any{message(fmt.Sprintf("approval-%d", n), "/allow "+match[1])}
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
			if strings.TrimSpace(text) == "WECHAT_SHARED_MAINPRINT_DONE" {
				finishOnce.Do(func() { close(done) })
			}
			if strings.TrimSpace(text) == "WECHAT_SHARED_MAINPRINT_RESUMED" {
				resumeOnce.Do(func() { close(resumed) })
			}
		default:
			t.Error("unexpected platform request", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	env := map[string]string{}
	for key, value := range o.Environment {
		env[key] = value
	}
	env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	env["WECHAT_BASE_URL"] = peer.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runtime *imruntime.WechatRuntime
	started := make(chan struct{})
	launcherDone := make(chan error, 1)
	go func() {
		code, err := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--wechat"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard, StartWechat: func(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
			config := imruntime.LoadPlatformConfig("wechat", home, o.WorkDir, options.Environment, func(string) {})
			runtime, err = imruntime.NewWechatRuntime(ctx, imruntime.WechatRuntimeOptions{Config: config, ServerHTTP: desktop.Client()})
			if err != nil {
				return nil, err
			}
			runtime.Start()
			close(started)
			return runtime, nil
		}})
		if err == nil && code != 0 {
			err = fmt.Errorf("launcher code %d", code)
		}
		launcherDone <- err
	}()
	select {
	case <-started:
	case err := <-launcherDone:
		t.Fatal("public launcher failed", err)
	case <-time.After(10 * time.Second):
		t.Fatal("adapter not started")
	}
	deadline := 30 * time.Second
	if liveToken != "" {
		deadline = 150 * time.Second
	}
	select {
	case <-done:
	case err := <-launcherDone:
		t.Fatal("adapter exited", err)
	case <-time.After(deadline):
		platformMu.Lock()
		snapshot := append([]string{}, notices...)
		platformMu.Unlock()
		t.Fatal("actual MainPrint reply missing", fakeRequests.Load(), approvals.Load(), snapshot)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "WECHAT_SHARED_MAINPRINT_FILE\n" || approvals.Load() < 1 {
		t.Fatal("actual Write/Read/permission file failed", err, approvals.Load())
	}
	if liveToken == "" && fakeRequests.Load() != 3 {
		t.Fatal("duplicate/alternate agent requests", fakeRequests.Load())
	}
	if liveToken != "" && fakeRequests.Load() != 0 {
		t.Fatal("live task used fake model")
	}
	stored := runtime.Store.Get("U1")
	if stored == nil {
		t.Fatal("actual session binding not persisted")
	}
	exists, err := runtime.Client.SessionExists(context.Background(), stored.SessionID)
	if err != nil || !exists {
		t.Fatal("same server session absent", err)
	}
	cancel()
	select {
	case err := <-launcherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("launcher did not join Close")
	}
	if liveToken == "" {
		// A real remote poll endpoint has its own request lifetime. The private
		// peer uses a destructive channel, so wait for the retired HTTP handler
		// to observe transport cancellation before publishing the next batch.
		// Otherwise the old peer can consume a message intended for restart.
		peerDrained := make(chan struct{})
		go func() { peerPolls.Wait(); close(peerDrained) }()
		select {
		case <-peerDrained:
		case <-time.After(10 * time.Second):
			t.Fatal("retired private getupdates request did not close")
		}
		restartCtx, restartCancel := context.WithCancel(context.Background())
		defer restartCancel()
		cfg := imruntime.LoadPlatformConfig("wechat", home, o.WorkDir, env, func(string) {})
		restarted, err := imruntime.NewWechatRuntime(restartCtx, imruntime.WechatRuntimeOptions{Config: cfg, ServerHTTP: desktop.Client()})
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		updates <- []any{message("resume", "Read the existing file, then finish.")}
		restarted.Start()
		select {
		case <-resumed:
		case <-time.After(30 * time.Second):
			platformMu.Lock()
			snapshot := append([]string{}, notices...)
			platformMu.Unlock()
			t.Fatal("restart did not continue old session", "modelRequests", fakeRequests.Load(), "approvals", approvals.Load(), "binding", restarted.Store.Get("U1"), "bridgeOpen", restarted.Bridge.IsOpen("U1", stored.SessionID), "notices", snapshot)
		}
		binding := restarted.Store.Get("U1")
		if binding == nil || binding.SessionID != stored.SessionID || fakeRequests.Load() != 5 {
			t.Fatal("restart changed original session/Harness", fakeRequests.Load())
		}
		restartCancel()
		restarted.Close()
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	if err := server.Close(closeCtx); err != nil {
		t.Fatal("shared MainPrint close", err)
	}
	if liveToken != "" {
		canonical := []byte{}
		_ = filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && entry.Name() == stored.SessionID+".jsonl" {
				canonical, _ = os.ReadFile(path)
			}
			return nil
		})
		if bytes.Contains(canonical, []byte(liveToken)) {
			t.Fatal("credential in canonical")
		}
		for _, marker := range []string{`"name":"Write"`, `"name":"Read"`, "WECHAT_SHARED_MAINPRINT_DONE"} {
			if !bytes.Contains(canonical, []byte(marker)) {
				t.Fatal("live canonical lacks", marker)
			}
		}
		receiptRoot := os.Getenv("CC_HAHA_WECHAT_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(receiptRoot) {
			t.Fatal("private live receipt root required")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop config + ordinary Go launcher + private Wechat HTTP protocol peer + shared Go MainPrint official DeepSeek Write/Read + actual IM permission + same session + Close; no real Wechat account/message claim", "endpoint": "https://api.deepseek.com/anthropic", "model": "deepseek-flash[1m]", "sessionId": stored.SessionID, "actualTools": []string{"Write", "Read"}, "catalog": "ordinary default, no forced Tools list", "fakeModelRequests": fakeRequests.Load(), "actualPermissionCallbacks": approvals.Load(), "actualFileExact": true, "credentialInCanonical": false}
		trace, traceErr := os.ReadFile(filepath.Join(config, "cc-haha", "traces", stored.SessionID+".jsonl"))
		receipt["rawTracePersisted"] = traceErr == nil && len(trace) > 0
		if traceErr == nil && len(trace) > 0 {
			if err := os.WriteFile(filepath.Join(receiptRoot, "trace-private.jsonl"), trace, 0600); err != nil {
				t.Fatal(err)
			}
		}
		for name, value := range map[string][]byte{"receipt.json": raw(receipt), "artifact.txt": data, "canonical.jsonl": canonical} {
			if err := os.WriteFile(filepath.Join(receiptRoot, name), value, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
