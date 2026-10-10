package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicGoCLIAdapterDefaultWechatFactoryActualPollPairingReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "WECHAT_ACCOUNT_ID", "WECHAT_BOT_TOKEN", "WECHAT_USER_ID", "NODE_EXTRA_CA_CERTS"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"wechat":{"accountId":"private-account","botToken":"private-token","allowedUsers":[]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	replied, blocked, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var replyOnce, pollOnce, closeOnce sync.Once
	polls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" {
			t.Error("original Wechat uses POST", req.Method)
		}
		if req.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("credential did not reach protocol")
		}
		var body protocol.JSONObject
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/ilink/bot/getupdates":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			if n == 1 {
				_, _ = w.Write([]byte(`{"ret":0,"get_updates_buf":"private-cursor","msgs":[{"from_user_id":"stranger","message_id":"private-first","seq":1,"context_token":"private-context","item_list":[{"type":1,"text_item":{"text":"hello"}}]}]}`))
				return
			}
			if body.String("get_updates_buf") != "private-cursor" {
				t.Error("original polling cursor lost")
			}
			pollOnce.Do(func() { close(blocked) })
			<-req.Context().Done()
			closeOnce.Do(func() { close(closed) })
		case "/ilink/bot/sendmessage":
			message := protocol.NewJSONObject()
			_ = message.UnmarshalJSON(body.Get("msg"))
			if message.String("context_token") != "private-context" {
				t.Error("admission context not available to unauthorized reply")
			}
			var items []protocol.JSONObject
			_ = json.Unmarshal(message.Get("item_list"), &items)
			if len(items) != 1 {
				t.Error("missing text item")
				return
			}
			var text protocol.JSONObject
			_ = text.UnmarshalJSON(items[0].Get("text_item"))
			if text.String("text") != "未授权。请先在 Claude Code 桌面端完成微信扫码绑定，再生成 IM 配对码后发送给我。" {
				t.Error("original Wechat pairing gate was bypassed")
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
			replyOnce.Do(func() { close(replied) })
		default:
			t.Error("unexpected platform request", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	t.Setenv("WECHAT_BASE_URL", peer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--wechat"})
	}()
	for _, item := range []struct {
		name string
		ch   <-chan struct{}
	}{{"ordinary reply", replied}, {"next actual long poll", blocked}} {
		select {
		case <-item.ch:
		case <-time.After(5 * time.Second):
			t.Fatal(item.name, "was not reached")
		}
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public Wechat did not Close and join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("platform long poll survived Close")
	}
	stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(seed) {
		t.Fatal("ordinary startup mutated original settings")
	}
}
