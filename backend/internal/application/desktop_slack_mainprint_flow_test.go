package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slackPrivateFilesTransport struct {
	destination string
	next        http.RoundTripper
}

func (tr slackPrivateFilesTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "files.slack.com" {
		return tr.next.RoundTrip(r)
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	copy.URL = &u
	copy.URL.Scheme = "http"
	copy.URL.Host = strings.TrimPrefix(tr.destination, "http://")
	copy.Host = "files.slack.com"
	return tr.next.RoundTrip(copy)
}
func TestActualDesktopSlackConfigLauncherTrustedSocketSharedMainPrintPermissionsAttachmentsWriteReadReplyClose(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		name := "text"
		if withFile {
			name = "incoming-file"
		}
		t.Run(name, func(t *testing.T) { actualDesktopSlackMainprint(t, withFile, "") })
	}
}
func TestActualSlackOfficialDeepSeekLiveModel(t *testing.T) {
	token := os.Getenv("CC_HAHA_SLACK_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_SLACK_LIVE_GATE") != "1" {
		t.Fatal("unowned live gate")
	}
	actualDesktopSlackMainprint(t, false, token)
}
func actualDesktopSlackMainprint(t *testing.T, withFile bool, liveToken string) {
	var posts, approvals, downloads, uploads, acks atomic.Int32
	var runtime *imruntime.SlackRuntime
	target, downloaded, imagePath := "", "", ""
	var modelMu sync.Mutex
	modelBodies := []json.RawMessage{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelMu.Lock()
		modelBodies = append(modelBodies, body)
		modelMu.Unlock()
		switch posts.Add(1) {
		case 1:
			response(w, "slack-write", "Write", map[string]string{"file_path": target, "content": "SLACK_ACTUAL_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "slack-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			if withFile {
				s := runtime.Store.Get("D1")
				if s == nil {
					t.Error("no actual binding before download")
				}
				downloaded = filepath.Join(runtime.Attachments.Root, "slack", s.SessionID, "private-input.txt")
				response(w, "slack-attachment-read", "Read", map[string]string{"file_path": downloaded}, "")
			} else {
				response(w, "slack-final", "", nil, "SLACK_ACTUAL_MAINPRINT_DONE\n![private image]("+imagePath+")")
			}
		case 4:
			if withFile {
				response(w, "slack-final", "", nil, "SLACK_ACTUAL_MAINPRINT_DONE\n![private image]("+imagePath+")")
			} else {
				response(w, "slack-restart-read", "Read", map[string]string{"file_path": target}, "")
			}
		case 5:
			response(w, "slack-restart-final", "", nil, "SLACK_RESTORED_MAINPRINT_DONE")
		default:
			t.Error("duplicate/extra model request")
			http.Error(w, "extra", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-slack-local-server"
	target = filepath.Join(o.WorkDir, "slack-mainprint-file.txt")
	imagePath = filepath.Join(o.WorkDir, "slack-outbound.png")
	_ = os.WriteFile(imagePath, []byte{137, 80, 78, 71, 13, 10, 26, 10}, 0600)
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "adapters.json"), []byte(`{"future":{"preserve":true},"telegram":{"botToken":"fake-other-token","futureTelegram":7}}`), 0600)
	if liveToken != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Arguments.Tools = []string{"Read,Write"}
		o.Environment["CLAUDE_CODE_DISABLE_THINKING"] = "1"
		o.Environment["ENABLE_TOOL_SEARCH"] = "false"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
		dir := filepath.Join(config, "cc-haha")
		_ = os.MkdirAll(dir, 0700)
		provider := map[string]any{"id": "slack-official-validation", "presetId": "deepseek", "name": "Private Slack official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		index := map[string]any{"schemaVersion": 6, "activeId": "slack-official-validation", "providers": []any{provider}, "providerOrder": []string{"slack-official-validation"}}
		_ = os.WriteFile(filepath.Join(dir, "providers.json"), raw(index), 0600)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s, err := NewDesktopServer(ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	desktop := httptest.NewUnstartedServer(s)
	desktop.Listener.Close()
	desktop.Listener = listener
	desktop.Start()
	defer desktop.Close()
	request := func(method, path string, body any) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, desktop.URL+path, bytes.NewReader(raw(body)))
		req.Header.Set("Authorization", "Bearer private-slack-local-server")
		req.Header.Set("Content-Type", "application/json")
		res, err := desktop.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("actual desktop config %s status%d %s", path, res.StatusCode, data)
		}
		return data
	}
	manifest := request("GET", "/api/adapters/slack/manifest", nil)
	if !bytes.Contains(manifest, []byte("socket_mode_enabled")) {
		t.Fatal("UI manifest consumer missing")
	}
	saved := request("PUT", "/api/adapters", map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "slack": map[string]any{"botToken": "private-slack-bot", "appToken": "private-slack-app", "allowedUsers": []string{"U1"}}})
	if bytes.Contains(saved, []byte("private-slack-bot")) || !bytes.Contains(saved, []byte(`"preserve":true`)) {
		t.Fatal("actual masked config/legacy fields lost")
	}
	incoming := make(chan any, 8)
	event := func(key, text, thread string, files []any) any {
		m := map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "user": "U1", "text": text, "client_msg_id": key, "thread_ts": thread}
		if len(files) > 0 {
			m["subtype"] = "file_share"
			m["files"] = files
		}
		return map[string]any{"type": "events_api", "envelope_id": key, "payload": map[string]any{"event": m}}
	}
	text := "Write and read the requested private artifact then finish."
	if liveToken != "" {
		text = fmt.Sprintf("Use Write to write %s with exact content SLACK_ACTUAL_MAINPRINT_FILE followed by a newline. Then use Read to verify that same file. Finally respond with only SLACK_ACTUAL_MAINPRINT_DONE. Do not use Bash or delegate.", target)
	}
	files := []any{}
	if withFile {
		files = append(files, map[string]string{"name": "private-input.txt", "mimetype": "text/plain", "url_private": "https://files.slack.com/incoming"})
	}
	first := event("first", text, "parent-1", files)
	incoming <- first
	incoming <- first
	socketClosed := make(chan struct{}, 2)
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		localDone := make(chan struct{})
		defer close(localDone)
		defer func() { socketClosed <- struct{}{} }()
		go func() {
			for {
				select {
				case message := <-incoming:
					if websocket.JSON.Send(conn, message) != nil {
						return
					}
				case <-localDone:
					return
				}
			}
		}()
		for {
			var m protocol.JSONObject
			if websocket.JSON.Receive(conn, &m) != nil {
				return
			}
			if m.String("envelope_id") != "" {
				acks.Add(1)
			}
		}
	}}
	socketPeer := httptest.NewTLSServer(ws)
	defer socketPeer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(socketPeer.Certificate())
	done, imageDone, restoredDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var finalOnce, imageOnce, restoredOnce sync.Once
	var messages atomic.Int32
	apiPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "files.slack.com" {
			switch r.URL.Path {
			case "/incoming":
				downloads.Add(1)
				if r.Header.Get("Authorization") != "Bearer private-slack-bot" {
					t.Error("download token authority lost")
				}
				_, _ = io.WriteString(w, "SLACK_ACTUAL_DOWNLOADED_DOCUMENT\n")
			case "/upload":
				uploads.Add(1)
				if r.Method != "POST" || r.Header.Get("Authorization") != "" {
					t.Error("external upload authority changed")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte("uploaded"))
			default:
				t.Error("unowned file path")
			}
			return
		}
		var m protocol.JSONObject
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&m)
		}
		reply := map[string]any{"ok": true}
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "auth.test":
			reply["user_id"] = "UBOT"
		case "apps.connections.open":
			if r.Header.Get("Authorization") != "Bearer private-slack-app" {
				t.Error("app-token authority changed")
			}
			reply["url"] = "wss" + strings.TrimPrefix(socketPeer.URL, "https")
		case "users.info":
			reply["user"] = map[string]string{"real_name": "Private User"}
		case "chat.postMessage", "chat.update":
			reply["ts"] = fmt.Sprintf("%d.1", messages.Add(1))
			if strings.HasPrefix(m.String("text"), "🔐 需要权限确认") {
				n := approvals.Add(1)
				incoming <- event(fmt.Sprintf("approve-%d", n), "2", "parent-approval", nil)
			}
			if strings.Contains(m.String("text"), "SLACK_ACTUAL_MAINPRINT_DONE") {
				finalOnce.Do(func() { close(done) })
			}
			if strings.Contains(m.String("text"), "SLACK_RESTORED_MAINPRINT_DONE") {
				if m.String("thread_ts") != "parent-restart" {
					t.Error("restored reply lost original thread context")
				}
				restoredOnce.Do(func() { close(restoredDone) })
			}
		case "files.getUploadURLExternal":
			reply["upload_url"] = "https://files.slack.com/upload"
			reply["file_id"] = "F1"
		case "files.completeUploadExternal":
			imageOnce.Do(func() { close(imageDone) })
		default:
			t.Error("unowned actual Slack method", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer apiPeer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	env["CALLER_DIR"] = o.WorkDir
	launcherDone := make(chan error, 1)
	go func() {
		code, err := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--slack"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard, StartSlack: func(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
			cfg := imruntime.LoadPlatformConfig("slack", home, options.WorkDir, options.Environment, func(string) {})
			api, err := imruntime.NewSlackAPI(imruntime.SlackAPIOptions{Token: cfg.Token, BaseURL: apiPeer.URL + "/", HTTP: &http.Client{Transport: slackPrivateFilesTransport{apiPeer.URL, http.DefaultTransport}}})
			if err != nil {
				return nil, err
			}
			runtime, err = imruntime.NewSlackRuntime(ctx, imruntime.SlackRuntimeOptions{Config: cfg, API: api, ServerHTTP: desktop.Client(), SocketTLS: &tls.Config{RootCAs: roots}})
			if err != nil {
				return nil, err
			}
			runtime.Start()
			return runtime, nil
		}})
		if err == nil && code != 0 {
			err = fmt.Errorf("launcher exit %d", code)
		}
		launcherDone <- err
	}()
	timeout := 20 * time.Second
	if liveToken != "" {
		timeout = 90 * time.Second
	}
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("actual Slack/MainPrint did not complete", posts.Load(), approvals.Load())
	}
	if liveToken == "" {
		select {
		case <-imageDone:
		case <-time.After(3 * time.Second):
			t.Fatal("actual outbound image upload missing")
		}
	}
	binding := runtime.Store.Get("D1")
	if binding == nil {
		t.Fatal("actual shared session binding absent")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "SLACK_ACTUAL_MAINPRINT_FILE\n" {
		t.Fatal("actual Write file mismatch", err)
	}
	if approvals.Load() < 1 {
		t.Fatal("actual default-mode approval absent")
	}
	if acks.Load() < 2 {
		t.Fatal("real Slack ACK missing")
	}
	if withFile {
		if downloads.Load() != 1 {
			t.Fatal("deferred attachment was downloaded before dedup", downloads.Load())
		}
		if downloaded == "" {
			t.Fatal("actual downloaded file not read")
		}
		modelMu.Lock()
		firstRequest := append([]byte{}, modelBodies[0]...)
		modelMu.Unlock()
		if !bytes.Contains(firstRequest, []byte("private-input.txt")) {
			t.Fatal("actual inbound file did not reach shared model context")
		}
		value, _ := os.ReadFile(downloaded)
		if string(value) != "SLACK_ACTUAL_DOWNLOADED_DOCUMENT\n" {
			t.Fatal("attachment Write/Read bridge missing")
		}
	}
	cancel()
	select {
	case err := <-launcherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual launcher Close not joined")
	}
	select {
	case <-socketClosed:
	case <-time.After(time.Second):
		t.Fatal("actual socket alive after Close")
	}
	if liveToken == "" && !withFile {
		restartCtx, restartCancel := context.WithCancel(context.Background())
		defer restartCancel()
		cfg := imruntime.LoadPlatformConfig("slack", home, o.WorkDir, env, func(string) {})
		restartAPI, err := imruntime.NewSlackAPI(imruntime.SlackAPIOptions{Token: cfg.Token, BaseURL: apiPeer.URL + "/", HTTP: &http.Client{Transport: slackPrivateFilesTransport{apiPeer.URL, http.DefaultTransport}}})
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := imruntime.NewSlackRuntime(restartCtx, imruntime.SlackRuntimeOptions{Config: cfg, API: restartAPI, ServerHTTP: desktop.Client(), SocketTLS: &tls.Config{RootCAs: roots}})
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		incoming <- event("restart", "Read the existing artifact and continue the previous session.", "parent-restart", nil)
		restarted.Start()
		select {
		case <-restoredDone:
		case <-time.After(10 * time.Second):
			t.Fatal("actual Slack restart did not restore", posts.Load())
		}
		restored := restarted.Store.Get("D1")
		if restored == nil || restored.SessionID != binding.SessionID {
			t.Fatal("Slack restart invented new binding", restored, binding)
		}
		modelMu.Lock()
		history := append([]byte{}, modelBodies[len(modelBodies)-2]...)
		modelMu.Unlock()
		if !bytes.Contains(history, []byte("SLACK_ACTUAL_MAINPRINT_FILE")) || !bytes.Contains(history, []byte("Write")) {
			t.Fatal("original tool history absent after adapter restart")
		}
		restartCancel()
		restarted.Close()
		select {
		case <-socketClosed:
		case <-time.After(time.Second):
			t.Fatal("restored socket not closed")
		}
	}
	unbound := request("POST", "/api/adapters/slack/unbind", nil)
	if bytes.Contains(unbound, []byte("****-bot")) || !bytes.Contains(unbound, []byte(`"futureTelegram":7`)) {
		t.Fatal("actual unbind consumer lost unknowns")
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatal("actual MainPrint Close", err)
	}
	canonicalFiles, err := filepath.Glob(filepath.Join(config, "projects", "*", binding.SessionID+".jsonl"))
	if err != nil || len(canonicalFiles) != 1 {
		t.Fatal("actual canonical file absent", binding.SessionID)
	}
	canonical, _ := os.ReadFile(canonicalFiles[0])
	if !bytes.Contains(canonical, []byte("SLACK_ACTUAL_MAINPRINT_DONE")) || !bytes.Contains(canonical, []byte("Write")) || !bytes.Contains(canonical, []byte("Read")) {
		t.Fatal("shared Harness canonical did not contain actual tools/reply")
	}
	if liveToken != "" {
		if posts.Load() != 0 {
			t.Fatal("official model task touched fixture model")
		}
		if bytes.Contains(canonical, []byte(liveToken)) {
			t.Fatal("credential entered canonical")
		}
		root := os.Getenv("CC_HAHA_SLACK_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(root) {
			t.Fatal("owned receipt path missing")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop config + native Go Slack Socket Mode private incoming + official DeepSeek shared MainPrint Write/Read + actual permissions + private Slack reply + Close; no real Slack account or messages", "endpoint": "https://api.deepseek.com/anthropic", "model": "deepseek-flash[1m]", "sessionId": binding.SessionID, "actualTools": []string{"Write", "Read"}, "actualFileExact": true, "fakeModelRequests": posts.Load(), "actualPermissionCallbacks": approvals.Load(), "actualSlackAcknowledgements": acks.Load(), "actualSlackSocketClosed": true, "credentialInCanonical": false}
		if err := os.WriteFile(filepath.Join(root, "receipt.json"), raw(receipt), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("official Slack shared MainPrint completed", binding.SessionID)
	} else {
		want := int32(5)
		if withFile {
			want = 4
		}
		if posts.Load() != want || uploads.Load() != 1 {
			t.Fatal("actual duplicate/final request count", posts.Load(), uploads.Load())
		}
	}

}
