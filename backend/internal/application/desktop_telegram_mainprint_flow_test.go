package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestActualDesktopAdapterConfigLauncherTelegramPollingSharedMainPrintPermissionsWriteReadReplyAndClose(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		name := "text"
		if withFile {
			name = "incoming-file"
		}
		t.Run(name, func(t *testing.T) { actualDesktopTelegramMainprint(t, withFile, false, "") })
	}
	t.Run("incoming-image", func(t *testing.T) { actualDesktopTelegramMainprint(t, false, true, "") })
}

func TestActualTelegramOfficialDeepSeekLiveModel(t *testing.T) {
	token := os.Getenv("CC_HAHA_TELEGRAM_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_TELEGRAM_LIVE_GATE") != "1" {
		t.Fatal("unowned live model gate")
	}
	actualDesktopTelegramMainprint(t, false, false, token)
}

func actualDesktopTelegramMainprint(t *testing.T, withFile, withImage bool, liveToken string) {
	var posts atomic.Int32
	var modelMu sync.Mutex
	modelBodies := []json.RawMessage{}
	target := ""
	downloaded := ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		modelMu.Lock()
		modelBodies = append(modelBodies, wire)
		modelMu.Unlock()
		switch posts.Add(1) {
		case 1:
			response(w, "telegram-write", "Write", map[string]string{"file_path": target, "content": "TELEGRAM_ACTUAL_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "telegram-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			if withFile {
				response(w, "telegram-read-download", "Read", map[string]string{"file_path": downloaded}, "")
			} else {
				response(w, "telegram-final", "", nil, "TELEGRAM_ACTUAL_MAINPRINT_DONE")
			}
		case 4:
			if withFile {
				response(w, "telegram-final", "", nil, "TELEGRAM_ACTUAL_MAINPRINT_DONE")
			} else {
				response(w, "telegram-restart-read", "Read", map[string]string{"file_path": target}, "")
			}
		case 5:
			response(w, "telegram-restart-final", "", nil, "TELEGRAM_RESTORED_MAINPRINT_DONE")
		default:
			t.Error("unexpected second Harness/duplicate incoming model request")
			http.Error(w, "extra model request", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	if withImage {
		o.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	}
	o.Arguments.PermissionMode = "default"
	if liveToken != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Arguments.Tools = []string{"Read,Write"}
		o.Environment["CLAUDE_CODE_DISABLE_THINKING"] = "1"
		o.Environment["ENABLE_TOOL_SEARCH"] = "false"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
	}
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-telegram-server-token"
	target = filepath.Join(o.WorkDir, "telegram-mainprint-file.txt")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	downloaded = filepath.Join(home, ".claude", "im-downloads", "telegram", "42", "private-input.txt")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"future":{"preserve":true},"feishu":{"appId":"other","appSecret":"fake-other-secret","future":7}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if liveToken != "" {
		dir := filepath.Join(config, "cc-haha")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		provider := map[string]any{"id": "telegram-official-validation", "presetId": "deepseek", "name": "Private Telegram official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		index := map[string]any{"schemaVersion": 6, "activeId": "telegram-official-validation", "providers": []any{provider}, "providerOrder": []string{"telegram-official-validation"}}
		if err := os.WriteFile(filepath.Join(dir, "providers.json"), raw(index), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	s, err := NewDesktopServer(ServerOptions{Port: port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if err == nil {
			n.ImageLibraryPath = o.Native.ImageLibraryPath
		}
		return n, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	desktop := httptest.NewUnstartedServer(s)
	_ = desktop.Listener.Close()
	desktop.Listener = listener
	desktop.Start()
	defer desktop.Close()
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "telegram": map[string]any{"botToken": "private-telegram-token", "allowedUsers": []int{42}}}
	request, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(raw(patch)))
	request.Header.Set("Authorization", "Bearer private-telegram-server-token")
	request.Header.Set("Content-Type", "application/json")
	saved, err := desktop.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(saved.Body)
	_ = saved.Body.Close()
	if saved.StatusCode != 200 || !bytes.Contains(body, []byte(`"preserve":true`)) || bytes.Contains(body, []byte("private-telegram-token")) {
		t.Fatalf("actual desktop config consumer: status%d %s", saved.StatusCode, body)
	}
	updates := make(chan []any, 8)
	makeIncoming := func(id int, text string) any {
		return map[string]any{"update_id": id, "message": map[string]any{"message_id": id, "date": 1, "chat": map[string]any{"id": 42, "type": "private"}, "from": map[string]any{"id": 42, "is_bot": false, "first_name": "Private"}, "text": text}}
	}
	incoming := makeIncoming(1, "Write the requested private artifact, read it, then finish.")
	if liveToken != "" {
		incoming = makeIncoming(1, fmt.Sprintf("Use the Write tool to write %s with exact content TELEGRAM_ACTUAL_MAINPRINT_FILE followed by a newline. Then use Read to verify that same file. Finally respond with only TELEGRAM_ACTUAL_MAINPRINT_DONE. Do not use Bash or delegate this task.", target))
	}
	if withFile {
		message := incoming.(map[string]any)["message"].(map[string]any)
		message["caption"] = message["text"]
		delete(message, "text")
		message["document"] = map[string]any{"file_id": "private-incoming-file", "file_unique_id": "private-unique", "file_name": "private-input.txt", "mime_type": "text/plain"}
	}
	var jpegData bytes.Buffer
	if withImage {
		if err := jpeg.Encode(&jpegData, image.NewNRGBA(image.Rect(0, 0, 17, 11)), nil); err != nil {
			t.Fatal(err)
		}
		message := incoming.(map[string]any)["message"].(map[string]any)
		message["caption"] = message["text"]
		delete(message, "text")
		message["photo"] = []any{map[string]any{"file_id": "private-incoming-image", "file_unique_id": "private-photo", "width": 17, "height": 11}}
	}
	updates <- []any{incoming, incoming}
	done := make(chan struct{})
	restartedDone := make(chan struct{})
	var restartCompleted sync.Once
	var completed sync.Once
	pollAborted := make(chan struct{})
	var aborted sync.Once
	var telegramMu sync.Mutex
	apiCalls := []protocol.JSONObject{}
	var approvals atomic.Int32
	var callbackID atomic.Int32
	var messages atomic.Int32
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/file/botprivate-telegram-token/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("server Bearer leaked into Telegram media fetch")
			}
			if withImage {
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(jpegData.Bytes())
			} else {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "TELEGRAM_ACTUAL_DOWNLOADED_DOCUMENT\n")
			}
			return
		}
		var payload protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		telegramMu.Lock()
		apiCalls = append(apiCalls, protocol.NewJSONObject(protocol.JSONField{Name: "method", Value: raw(method)}, protocol.JSONField{Name: "payload", Value: raw(payload)}))
		telegramMu.Unlock()
		result := any(true)
		switch method {
		case "getFile":
			result = map[string]any{"file_path": "files/private-input.txt"}
		case "getMe":
			result = map[string]any{"id": 999, "is_bot": true, "first_name": "Private", "username": "private_telegram_bot"}
		case "getUpdates":
			if string(payload.Get("limit")) == "1" {
				result = []any{}
			} else {
				select {
				case batch := <-updates:
					result = batch
				case <-r.Context().Done():
					aborted.Do(func() { close(pollAborted) })
					return
				}
			}
		case "sendMessage":
			result = map[string]any{"message_id": messages.Add(1)}
			var markup struct {
				Inline [][]map[string]string `json:"inline_keyboard"`
			}
			_ = json.Unmarshal(payload.Get("reply_markup"), &markup)
			if len(markup.Inline) > 0 && len(markup.Inline[0]) > 1 {
				data := markup.Inline[0][1]["callback_data"]
				if strings.HasPrefix(data, "permit:") && strings.HasSuffix(data, ":always") {
					n := callbackID.Add(1)
					approvals.Add(1)
					callback := map[string]any{
						"id":   "private-permission-" + strconv.Itoa(int(n)),
						"from": map[string]any{"id": 42, "first_name": "Private", "is_bot": false},
						"message": map[string]any{
							"message_id": result.(map[string]any)["message_id"], "date": 1,
							"chat": map[string]any{"id": 42, "type": "private"}, "text": payload.String("text"),
						},
						"data": data,
					}
					updates <- []any{map[string]any{"update_id": 100 + n, "callback_query": callback}}
				}
			}
		case "editMessageText":
			if payload.String("text") == "TELEGRAM_ACTUAL_MAINPRINT_DONE" {
				completed.Do(func() { close(done) })
			}
			if payload.String("text") == "TELEGRAM_RESTORED_MAINPRINT_DONE" {
				restartCompleted.Do(func() { close(restartedDone) })
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(map[string]any{"ok": true, "result": result}))
	}))
	defer telegram.Close()
	env := map[string]string{}
	for key, value := range o.Environment {
		env[key] = value
	}
	env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runtime *imruntime.TelegramRuntime
	started := make(chan struct{})
	launcherDone := make(chan error, 1)
	var stderr bytes.Buffer
	go func() {
		code, err := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--telegram"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: &stderr, StartTelegram: func(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
			config := imruntime.LoadConfig(options.HomeDir, options.WorkDir, options.Environment, func(string) {})
			api, err := imruntime.NewTelegramAPI(imruntime.TelegramAPIOptions{Token: config.Token, APIRoot: telegram.URL, HTTP: telegram.Client()})
			if err != nil {
				return nil, err
			}
			runtime, err = imruntime.NewTelegramRuntime(ctx, imruntime.TelegramRuntimeOptions{Config: config, API: api, ServerHTTP: desktop.Client(), MediaHTTP: telegram.Client()})
			if err != nil {
				return nil, err
			}
			runtime.Start()
			close(started)
			return runtime, nil
		}})
		if err == nil && code != 0 {
			err = fmt.Errorf("launcher code%d", code)
		}
		launcherDone <- err
	}()
	select {
	case <-started:
	case err := <-launcherDone:
		t.Fatal("actual launcher failed", err, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("actual adapter did not start")
	}
	deadline := 30 * time.Second
	if liveToken != "" {
		deadline = 150 * time.Second
	}
	select {
	case <-done:
	case err := <-launcherDone:
		t.Fatal("actual poller ended", err, stderr.String())
	case <-time.After(deadline):
		telegramMu.Lock()
		snapshot := raw(apiCalls)
		telegramMu.Unlock()
		t.Fatal("actual MainPrint reply missing", posts.Load(), approvals.Load(), string(snapshot))
	}
	data, err := os.ReadFile(target)
	expectedPosts := int32(3)
	if liveToken != "" {
		expectedPosts = 0
	}
	if withFile {
		expectedPosts = 4
	}
	if err != nil || string(data) != "TELEGRAM_ACTUAL_MAINPRINT_FILE\n" || posts.Load() != expectedPosts || approvals.Load() == 0 {
		t.Fatal("actual same-session tools/approval", err, posts.Load(), approvals.Load())
	}
	stored := runtime.Store.Get("42")
	if stored == nil {
		t.Fatal("actual session binding not persisted")
	}
	exists, err := runtime.Client.SessionExists(context.Background(), stored.SessionID)
	if err != nil || !exists {
		t.Fatal("adapter session not safe/recoverable", err, exists)
	}
	modelMu.Lock()
	if withImage {
		if !bytes.Contains(modelBodies[0], []byte(`"type":"image"`)) || !bytes.Contains(modelBodies[0], []byte(`"media_type":"image/jpeg"`)) {
			t.Error("actual Telegram photo did not reach the same native image model consumer")
		}
	}
	if liveToken == "" && (!bytes.Contains(modelBodies[1], []byte("call-telegram-write")) || !bytes.Contains(modelBodies[2], []byte("TELEGRAM_ACTUAL_MAINPRINT_FILE"))) {
		t.Error("actual tool results not returned to the same MainPrint")
	}
	if withFile {
		if !bytes.Contains(modelBodies[0], []byte("private-input.txt")) || !bytes.Contains(modelBodies[3], []byte("TELEGRAM_ACTUAL_DOWNLOADED_DOCUMENT")) {
			t.Error("real Telegram attachment did not reach the same model/Read consumer")
		}
		data, err := os.ReadFile(downloaded)
		if err != nil || string(data) != "TELEGRAM_ACTUAL_DOWNLOADED_DOCUMENT\n" {
			t.Error("real attachment stage", err)
		}
	}
	modelMu.Unlock()
	cancel()
	select {
	case err := <-launcherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("launcher Close did not join")
	}
	select {
	case <-pollAborted:
	case <-time.After(time.Second):
		t.Fatal("real Telegram long poll not cancelled")
	}
	if !strings.Contains(stderr.String(), "[claude-sidecar] starting Telegram adapter") {
		t.Fatal("original desktop launch producer missing")
	}
	if !withFile && !withImage && liveToken == "" {
		// Desktop configuration restart creates a new adapter process and restores
		// the exact existing server/CLI session instead of creating a transcript.
		restartCtx, restartCancel := context.WithCancel(context.Background())
		defer restartCancel()
		api, err := imruntime.NewTelegramAPI(imruntime.TelegramAPIOptions{Token: "private-telegram-token", APIRoot: telegram.URL, HTTP: telegram.Client()})
		if err != nil {
			t.Fatal(err)
		}
		config := imruntime.LoadConfig(home, o.WorkDir, env, func(string) {})
		restarted, err := imruntime.NewTelegramRuntime(restartCtx, imruntime.TelegramRuntimeOptions{Config: config, API: api, ServerHTTP: desktop.Client(), MediaHTTP: telegram.Client()})
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		updates <- []any{makeIncoming(1000, "Read the existing private artifact and finish.")}
		restarted.Start()
		select {
		case <-restartedDone:
		case <-time.After(20 * time.Second):
			t.Fatal("actual restart did not restore and complete", posts.Load())
		}
		binding := restarted.Store.Get("42")
		if binding == nil || binding.SessionID != stored.SessionID || posts.Load() != 5 {
			t.Fatal("adapter restart created a new session/Harness", binding, stored, posts.Load())
		}
		modelMu.Lock()
		if len(modelBodies) != 5 || !bytes.Contains(modelBodies[3], []byte("call-telegram-write")) || !bytes.Contains(modelBodies[4], []byte("TELEGRAM_ACTUAL_MAINPRINT_FILE")) {
			t.Error("actual resumed MainPrint lost prior tool history")
		}
		modelMu.Unlock()
		restartCancel()
		restarted.Close()
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	if err = s.Close(closeCtx); err != nil {
		t.Fatal("actual desktop MainPrint Close", err)
	}
	if liveToken != "" {
		actual := []byte{}
		_ = filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && entry.Name() == stored.SessionID+".jsonl" {
				actual, _ = os.ReadFile(path)
			}
			return nil
		})
		if bytes.Contains(actual, []byte(liveToken)) {
			t.Fatal("credential appeared in canonical model data")
		}
		if !bytes.Contains(actual, []byte(`"name":"Write"`)) || !bytes.Contains(actual, []byte(`"name":"Read"`)) || !bytes.Contains(actual, []byte("TELEGRAM_ACTUAL_MAINPRINT_DONE")) {
			t.Fatal("actual official-model tool/canonical evidence absent")
		}
		receiptRoot := os.Getenv("CC_HAHA_TELEGRAM_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(receiptRoot) {
			t.Fatal("owned live receipt root missing")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop config + Go Telegram polling private incoming + official DeepSeek shared MainPrint Write/Read + actual permissions + private Telegram API reply + Close; no real Telegram account or messages", "endpoint": "https://api.deepseek.com/anthropic", "model": "deepseek-flash[1m]", "sessionId": stored.SessionID, "actualTools": []string{"Write", "Read"}, "actualFileExact": true, "fakeModelRequests": posts.Load(), "actualPermissionCallbacks": approvals.Load(), "actualTelegramLongPollCancelled": true, "credentialInCanonical": false}
		if err := os.WriteFile(filepath.Join(receiptRoot, "receipt.json"), raw(receipt), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(receiptRoot, "artifact.txt"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(receiptRoot, "canonical.jsonl"), actual, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
