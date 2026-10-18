package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicGoCLIAdapterDefaultTelegramFactoryActualPollingReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	t.Setenv("CALLER_DIR", home)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	seed := []byte(`{"telegram":{"botToken":"private-cli-telegram-token","allowedUsers":[42]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	methods := []string{}
	first := true
	replied := make(chan struct{})
	blocked := make(chan struct{})
	aborted := make(chan struct{})
	commands := make(chan struct{})
	var replyOnce, blockOnce, abortOnce, commandsOnce sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var payload protocol.JSONObject
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		methods = append(methods, method)
		deliver := false
		if method == "getUpdates" && string(payload.Get("limit")) != "1" && first {
			first = false
			deliver = true
		}
		mu.Unlock()
		var result any = true
		switch method {
		case "getMe":
			result = map[string]any{"id": 999, "first_name": "Private", "is_bot": true, "username": "private_test_bot"}
		case "getUpdates":
			if string(payload.Get("limit")) == "1" {
				result = []any{}
			} else if deliver {
				result = []any{map[string]any{"update_id": 1, "message": map[string]any{"message_id": 1, "date": 1, "chat": map[string]any{"id": 42, "type": "private"}, "from": map[string]any{"id": 43, "first_name": "Private", "is_bot": false}, "text": "not paired"}}}
			} else {
				blockOnce.Do(func() { close(blocked) })
				<-r.Context().Done()
				abortOnce.Do(func() { close(aborted) })
				return
			}
		case "sendMessage":
			if payload.String("text") != "🔒 未授权。请在 Claude Code 桌面端生成配对码后发送给我。" {
				t.Error("public CLI did not execute the original pairing gate")
			}
			result = map[string]int{"message_id": 1}
			replyOnce.Do(func() { close(replied) })
		case "setMyCommands":
			commandsOnce.Do(func() { close(commands) })
		}
		w.Header().Set("Content-Type", "application/json")
		wire, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		_, _ = w.Write(wire)
	}))
	defer peer.Close()
	previous := newNativeTelegramAPI
	newNativeTelegramAPI = func(options imruntime.TelegramAPIOptions) (*imruntime.TelegramAPI, error) {
		if options.Token != "private-cli-telegram-token" || options.Environment["CLAUDE_CONFIG_DIR"] != config {
			t.Error("public entry lost actual adapter configuration")
		}
		options.APIRoot = peer.URL
		options.HTTP = peer.Client()
		return imruntime.NewTelegramAPI(options)
	}
	defer func() { newNativeTelegramAPI = previous }()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	stdout := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = stdout; _ = write.Close() }()
	output := make(chan string, 1)
	go func() { bytes, _ := io.ReadAll(read); output <- string(bytes) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--telegram"})
	}()
	select {
	case <-replied:
	case <-time.After(5 * time.Second):
		t.Fatal("real public CLI reply absent")
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("public CLI did not resume long polling")
	}
	// Source starts menu synchronization and polling independently. A reply or
	// the next long poll cannot prove that setMyCommands has already arrived.
	select {
	case <-commands:
	case <-time.After(5 * time.Second):
		t.Fatal("public CLI did not publish its actual command menu")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal("public entry exit", code)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("public CLI did not Close and join")
	}
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("native poll not cancelled")
	}
	_ = write.Close()
	os.Stdout = stdout
	logs := <-output
	if !strings.Contains(logs, "[claude-sidecar] starting Telegram adapter") || !strings.Contains(logs, "[Telegram] Starting bot...") {
		t.Fatal("Source launch producer not executed", logs)
	}
	mu.Lock()
	seen := map[string]bool{}
	for _, method := range methods {
		seen[method] = true
	}
	mu.Unlock()
	for _, method := range []string{"getMe", "deleteWebhook", "deleteMyCommands", "setMyCommands", "getUpdates", "sendMessage"} {
		if !seen[method] {
			t.Fatal("actual Telegram API consumer missing", method)
		}
	}
	stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err != nil || string(stored) != string(seed) {
		t.Fatal("public CLI rewrote legacy adapter fields", err)
	}
}
