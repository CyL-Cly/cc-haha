package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

type qqPublicPrivateTransport struct {
	base    *url.URL
	next    http.RoundTripper
	replied func()
}

func (t qqPublicPrivateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = t.base.Scheme
	u.Host = t.base.Host
	copy.URL = &u
	copy.Host = ""
	response, err := t.next.RoundTrip(copy)
	if err == nil && strings.HasSuffix(req.URL.Path, "/messages") {
		response.Body = &qqPublicReadEOF{ReadCloser: response.Body, done: t.replied}
	}
	return response, err
}

type qqPublicReadEOF struct {
	io.ReadCloser
	done func()
	once sync.Once
}

func (r *qqPublicReadEOF) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF {
		r.once.Do(r.done)
	}
	return n, err
}

func TestPublicGoCLIAdapterDefaultQQFactoryActualTokenGatewayInboundReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "QQ_APP_ID", "QQ_APP_SECRET"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"qq":{"appId":"private-app","appSecret":"private-secret","allowedUsers":[]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	replied, closed := make(chan struct{}), make(chan struct{})
	var replyOnce, closeOnce sync.Once
	peer := httptest.NewTLSServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); closeOnce.Do(func() { close(closed) }) }()
		_ = websocket.Message.Send(conn, `{"op":10,"d":{"heartbeat_interval":30000}}`)
		for {
			var frame protocol.JSONObject
			if websocket.JSON.Receive(conn, &frame) != nil {
				return
			}
			switch string(frame.Get("op")) {
			case "2":
				var data protocol.JSONObject
				_ = data.UnmarshalJSON(frame.Get("d"))
				if data.String("token") != "QQBot private-access-token" {
					t.Error("actual IDENTIFY token lost")
				}
				_ = websocket.Message.Send(conn, `{"op":0,"s":1,"t":"READY","d":{"session_id":"private-session"}}`)
				_ = websocket.Message.Send(conn, `{"op":0,"s":2,"t":"C2C_MESSAGE_CREATE","d":{"author":{"user_openid":"stranger"},"content":"hello","id":"private-incoming"}}`)
			case "1":
				_ = websocket.Message.Send(conn, `{"op":11}`)
			}
		}
	}})
	defer peer.Close()
	ca := filepath.Join(home, "private-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_EXTRA_CA_CERTS", ca)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body protocol.JSONObject
		if req.Method == "POST" {
			_ = json.NewDecoder(req.Body).Decode(&body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/app/getAppAccessToken":
			if body.String("appId") != "private-app" || body.String("clientSecret") != "private-secret" {
				t.Error("file credentials lost")
			}
			_, _ = w.Write([]byte(`{"access_token":"private-access-token","expires_in":7200}`))
		case "/gateway":
			_, _ = w.Write([]byte(`{"url":"wss` + strings.TrimPrefix(peer.URL, "https") + `"}`))
		case "/v2/users/stranger/messages":
			if req.Header.Get("Authorization") != "QQBot private-access-token" {
				t.Error("API credential not retained")
			}
			if body.String("content") != "未授权。请在 Claude Code 桌面端生成配对码后发送给我。" || body.String("msg_id") != "private-incoming" {
				t.Error("original pairing gate/target binding lost")
			}
			_, _ = w.Write([]byte(`{"id":"private-reply"}`))
		default:
			t.Error("unexpected actual QQ API", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	apiURL, _ := url.Parse(api.URL)
	previous := newNativeQQClient
	newNativeQQClient = func(ctx context.Context, options imruntime.QQClientOptions) (*imruntime.QQClient, error) {
		options.HTTP = &http.Client{Transport: qqPublicPrivateTransport{base: apiURL, next: api.Client().Transport, replied: func() { replyOnce.Do(func() { close(replied) }) }}}
		return imruntime.NewQQClient(ctx, options)
	}
	defer func() { newNativeQQClient = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() { finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--qq"}) }()
	select {
	case <-replied:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary QQ default factory did not reach original reply")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public QQ did not Close and join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual QQ socket survived Close")
	}
	stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err != nil || string(stored) != string(seed) {
		t.Fatal("ordinary startup mutated stored fields", err)
	}
}
