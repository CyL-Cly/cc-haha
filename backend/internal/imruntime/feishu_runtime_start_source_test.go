package imruntime

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestActualFeishuIndexStartupCredentialGateRetainsRawTruthyValues(t *testing.T) {
	for _, credentials := range []protocol.JSONObject{object(raw(map[string]any{"appId": "a"})), object(raw(map[string]any{"appId": 0, "appSecret": "s"})), object(raw(map[string]any{"appId": "a", "appSecret": false})), object(raw(map[string]any{"appId": 7, "appSecret": map[string]any{"raw": true}}))} {
		home := t.TempDir()
		work := filepath.Join(home, "work")
		os.MkdirAll(work, 0700)
		missing := !credentialTruthy(credentials.Get("appId")) || !credentialTruthy(credentials.Get("appSecret"))
		// Successful import allocates live SDK caches; the gate fixture is limited to
		// Source's rejection before any client/store producer is constructed.
		if missing {
			want := runPlatformStageSource(t, "native-feishu-runtime.fixture.ts", "CC_HAHA_FEISHU_RUNTIME_FIXTURE", map[string]any{"gate": true, "config": map[string]any{"feishu": credentials}, "server": "ws://127.0.0.1:1", "now": int64(1770000000000)}, home)
			exactJSON(t, want, raw(map[string]bool{"missing": true}), "original Feishu startup gate")
		}
		cfg := Config{Home: home, ConfigDir: filepath.Join(home, ".claude"), WorkDir: work, ServerURL: "ws://127.0.0.1:1", Environment: map[string]string{}}
		runtime, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Credentials: credentials})
		if (e != nil) != missing {
			t.Fatal("native credential truthiness changed", e)
		}
		if runtime != nil {
			runtime.Close()
		}
	}
}

func TestActualFeishuIndexStartRegistersActualBinaryPlatformEventsAndStopsSockets(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0700)
	config := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "feishu": map[string]any{"appId": "fixture-feishu", "appSecret": "fixture-secret", "allowedUsers": []string{"U1"}}}
	now := int64(1770000000000)
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		packets, wire := []any{}, []any{}
		notices := 0
		done, closed := make(chan struct{}), make(chan struct{})
		var once sync.Once
		socket := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(c *websocket.Conn) {
			defer c.Close()
			defer close(closed)
			var p []byte
			if websocket.Message.Receive(c, &p) != nil {
				return
			}
			f, e := DecodeFeishuFrame(p)
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			wire = append(wire, feishuFrameJSON(f))
			mu.Unlock()
			for i, user := range []string{"U2", "U1"} {
				event := map[string]any{"schema": "2.0", "header": map[string]string{"event_type": "im.message.receive_v1"}, "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": user}}, "message": map[string]any{"message_id": user, "chat_id": "chat-1", "chat_type": "p2p", "message_type": "text", "content": `{"text":"/help"}`}}}
				f := FeishuFrame{SeqID: uint64(i + 1), LogID: 4, Service: 9, Method: 1, Headers: []FeishuHeader{{"type", "event"}, {"message_id", user}, {"sum", "1"}, {"seq", "0"}}, Payload: raw(event)}
				c.PayloadType = websocket.BinaryFrame
				if websocket.Message.Send(c, EncodeFeishuFrame(f)) != nil {
					return
				}
				if websocket.Message.Receive(c, &p) != nil {
					return
				}
				ack, e := DecodeFeishuFrame(p)
				if e != nil {
					t.Error(e)
					return
				}
				mu.Lock()
				wire = append(wire, feishuFrameJSON(ack))
				mu.Unlock()
			}
			for websocket.Message.Receive(c, &p) == nil {
			}
		}}
		var peerURL string
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/socket" {
				socket.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/fixture/wait" {
				select {
				case <-done:
					w.Write([]byte(`{}`))
				case <-r.Context().Done():
				}
				return
			}
			p, _ := io.ReadAll(r.Body)
			packet := map[string]any{"path": r.URL.RequestURI(), "method": r.Method}
			if len(p) > 0 {
				packet["body"] = json.RawMessage(p)
			}
			mu.Lock()
			packets = append(packets, packet)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
			case "/open-apis/bot/v3/info":
				w.Write([]byte(`{"code":0,"bot":{"open_id":"bot-private"}}`))
			case "/callback/ws/endpoint":
				w.Write(raw(map[string]any{"code": 0, "data": map[string]any{"URL": "ws" + strings.TrimPrefix(peerURL, "http") + "/socket?device_id=private&service_id=9", "ClientConfig": map[string]any{"PingInterval": 1000, "ReconnectCount": 0}}}))
			case "/open-apis/im/v1/messages":
				w.Write([]byte(`{"code":0,"data":{"message_id":"notice"}}`))
				mu.Lock()
				notices++
				if notices == 2 {
					once.Do(func() { close(done) })
				}
				mu.Unlock()
			default:
				t.Errorf("unowned start endpoint %s", r.URL.Path)
				w.Write([]byte(`{}`))
			}
		}))
		peerURL = peer.URL
		defer peer.Close()
		var result json.RawMessage
		if source {
			os.MkdirAll(filepath.Join(home, ".claude"), 0700)
			os.WriteFile(filepath.Join(home, ".claude", "adapter-sessions.json"), []byte(`{}`), 0600)
			result = runPlatformStageSource(t, "native-feishu-runtime.fixture.ts", "CC_HAHA_FEISHU_RUNTIME_FIXTURE", map[string]any{"bootstrap": true, "config": config, "peer": peer.URL, "server": "ws://127.0.0.1:1", "now": now, "actions": []any{}}, home)
		} else {
			configDir := filepath.Join(home, ".claude")
			os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(config), 0600)
			cfg := LoadPlatformConfig("feishu", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "ADAPTER_SERVER_URL": "ws://127.0.0.1:1"}, func(string) {})
			client := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(now) }, "feishu")
			no := false
			runtime, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(now) }, WSOptions: FeishuWSOptions{AutoReconnect: &no}})
			if e != nil {
				t.Fatal(e)
			}
			if e = runtime.Start(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				runtime.Close()
				t.Fatal("actual registered platform event did not produce notices")
			}
			runtime.Close()
			result = raw(map[string]any{"rows": []any{}, "botOpenId": runtime.botOpenID, "sessions": runtime.Store.List()})
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("actual platform socket remained after Close")
		}
		mu.Lock()
		receipt := raw(map[string]any{"http": packets, "wire": wire})
		mu.Unlock()
		return result, receipt
	}
	want, wantWire := run(true)
	got, gotWire := run(false)
	exactJSON(t, got, want, "original index start identity and runtime cleanup")
	exactJSON(t, gotWire, wantWire, "original start actual SDK HTTP/binary registered callbacks")
}
