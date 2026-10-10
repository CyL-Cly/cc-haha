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

func TestActualFeishuIndexFIFOAdmissionDefersMediaAndUnauthorizedImageNeverDownloads(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0700)
	now := int64(1770000000000)
	config := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "feishu": map[string]any{"appId": "fixture-feishu", "appSecret": "fixture-secret", "allowedUsers": []string{"U1"}}}
	event := func(id, user, kind, content string) any {
		return map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": user}}, "message": map[string]any{"message_id": id, "chat_id": "chat-1", "chat_type": "p2p", "message_type": kind, "content": content}}
	}
	incoming := []any{event("image-first", "U1", "image", `{"image_key":"private-image"}`), event("text-second", "U1", "text", `{"text":"second"}`), event("unauthorized", "U2", "image", `{"image_key":"must-not-fetch"}`)}
	actions := []any{map[string]any{"kind": "ready"}, map[string]any{"kind": "server", "message": map[string]string{"type": "message_complete"}}}
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		packets, frames := []any{}, []any{}
		held, release, received := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(c *websocket.Conn) {
			defer c.Close()
			for {
				var m protocol.JSONObject
				if websocket.JSON.Receive(c, &m) != nil {
					return
				}
				mu.Lock()
				frames = append(frames, m)
				n := len(frames)
				mu.Unlock()
				if n == 2 {
					close(received)
				}
			}
		}}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/ws/") {
				ws.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/fixture/media-start" {
				select {
				case <-held:
					w.Write([]byte(`{}`))
				case <-r.Context().Done():
				}
				return
			}
			if r.URL.Path == "/fixture/release" {
				releaseOnce.Do(func() { close(release) })
				w.Write([]byte(`{}`))
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
			case "/open-apis/im/v1/messages/image-first/resources/private-image":
				close(held)
				select {
				case <-release:
					w.Write([]byte("private-image"))
				case <-r.Context().Done():
				}
			case "/open-apis/cardkit/v1/cards":
				w.Write([]byte(`{"code":0,"data":{"card_id":"card-private"}}`))
			case "/open-apis/im/v1/messages":
				w.Write([]byte(`{"code":0,"data":{"message_id":"message-private"}}`))
			default:
				if strings.HasPrefix(r.URL.Path, "/open-apis/cardkit/v1/cards/") {
					w.Write([]byte(`{"code":0}`))
					return
				}
				t.Errorf("unexpected FIFO endpoint %s", r.URL.Path)
				w.WriteHeader(404)
			}
		}))
		defer peer.Close()
		serverURL := "ws" + strings.TrimPrefix(peer.URL, "http")
		var result json.RawMessage
		if source {
			result = runPlatformStageSource(t, "native-feishu-runtime.fixture.ts", "CC_HAHA_FEISHU_RUNTIME_FIXTURE", map[string]any{"config": config, "peer": peer.URL, "server": serverURL, "seed": true, "work": work, "now": now, "concurrent": incoming, "actions": actions}, home)
		} else {
			os.RemoveAll(filepath.Join(home, ".claude", "im-downloads"))
			configDir := filepath.Join(home, ".claude")
			os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(config), 0600)
			cfg := LoadPlatformConfig("feishu", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "ADAPTER_SERVER_URL": serverURL}, func(string) {})
			client := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(now) }, "feishu")
			r, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(now) }, Streaming: FeishuStreamingOptions{UUID: func() string { return "fixture-runtime-uuid" }}})
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			r.Store.Set("chat-1", "session-1", work)
			r.Bridge.Connect("chat-1", "session-1", r.handler("chat-1"))
			if !r.Bridge.WaitOpen(context.Background(), "chat-1") {
				t.Fatal("actual session unavailable")
			}
			for _, event := range incoming {
				if e = r.HandleMessage(object(raw(event))); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case <-held:
			case <-time.After(time.Second):
				t.Fatal("admitted task did not reach deferred media")
			}
			mu.Lock()
			if len(frames) != 0 {
				t.Fatal("FIFO sent later input before earlier media completion")
			}
			mu.Unlock()
			releaseOnce.Do(func() { close(release) })
			if e = r.Drain(context.Background(), "chat-1"); e != nil {
				t.Fatal(e)
			}
			r.wg.Wait()
			s := r.State("chat-1")
			rows := []any{map[string]any{"error": "", "value": nil, "state": map[string]any{"state": s.State, "pendingPermissionCount": s.PendingPermissionCount, "pending": s.Pending}}}
			if e = r.HandleServerMessage("chat-1", object(raw(map[string]string{"type": "message_complete"}))); e != nil {
				t.Fatal(e)
			}
			s = r.State("chat-1")
			rows = append(rows, map[string]any{"error": "", "value": nil, "state": map[string]any{"state": s.State, "pendingPermissionCount": s.PendingPermissionCount, "pending": s.Pending}})
			result = raw(map[string]any{"rows": rows, "sessions": r.Store.List()})
		}
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatal("actual SessionWS FIFO frames missing")
		}
		mu.Lock()
		receipt := raw(map[string]any{"http": packets, "frames": frames})
		mu.Unlock()
		return result, receipt
	}
	want, wantPackets := run(true)
	got, gotPackets := run(false)
	exactJSON(t, got, want, "actual index asynchronous FIFO session/state")
	exactJSON(t, gotPackets, wantPackets, "actual index deferred media, unauthorized no-download and ordered SessionWS inputs")
}

func TestActualFeishuRuntimeCloseCancelsHeldMediaAndRejectsQueuedLaterInputs(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0700)
	configDir := filepath.Join(home, ".claude")
	os.MkdirAll(configDir, 0700)
	os.WriteFile(filepath.Join(configDir, "adapters.json"), []byte(`{"feishu":{"appId":"fixture-feishu","appSecret":"fixture-secret","allowedUsers":["U1"]}}`), 0600)
	held, cancelled := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	frames, downloads := 0, 0
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(c *websocket.Conn) {
		defer c.Close()
		for {
			var m protocol.JSONObject
			if websocket.JSON.Receive(c, &m) != nil {
				return
			}
			mu.Lock()
			frames++
			mu.Unlock()
		}
	}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			ws.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
		case "/open-apis/im/v1/messages/held/resources/image":
			// SDK resource GET has a JSON body. Consume it so the HTTP/1 server read
			// loop can observe the physical disconnect when the owner cancels.
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			downloads++
			mu.Unlock()
			close(held)
			<-r.Context().Done()
			close(cancelled)
		default:
			t.Errorf("new frame/API task during held-media shutdown: %s", r.URL.Path)
		}
	}))
	defer peer.Close()
	cfg := Config{Home: home, ConfigDir: configDir, WorkDir: work, ServerURL: "ws" + strings.TrimPrefix(peer.URL, "http"), Environment: map[string]string{}, Roots: []string{work}}
	client := feishuSourceClient(t, peer, time.Now, "feishu")
	r, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: peer.Client()})
	if e != nil {
		t.Fatal(e)
	}
	r.Store.Set("chat-1", "session-1", work)
	r.Bridge.Connect("chat-1", "session-1", r.handler("chat-1"))
	if !r.Bridge.WaitOpen(context.Background(), "chat-1") {
		t.Fatal("actual session socket missing")
	}
	event := func(id, kind, content string) protocol.JSONObject {
		return object(raw(map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": "U1"}}, "message": map[string]string{"message_id": id, "chat_id": "chat-1", "chat_type": "p2p", "message_type": kind, "content": content}}))
	}
	r.HandleMessage(event("held", "image", `{"image_key":"image"}`))
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("physical deferred download did not start")
	}
	r.HandleMessage(event("queued", "text", `{"text":"must not execute"}`))
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel/join held attachment and FIFO producers")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("actual network request not cancelled")
	}
	r.HandleMessage(event("after-close", "text", `{"text":"must not execute"}`))
	mu.Lock()
	defer mu.Unlock()
	if downloads != 1 || frames != 0 {
		t.Fatal("queued or post-Close input reached new download/model frame", downloads, frames)
	}
}
