package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestIMRecoveryAndActualHTTPWSAuthPermissionsPacketsAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	seed := map[string]any{"42": map[string]any{"sessionId": "exists", "workDir": work, "updatedAt": 1}}
	actions := []map[string]any{{"chat": "42", "restore": true}, {"chat": "42", "restore": true}, {"chat": "42", "send": true}, {"chat": "42", "allow": true}, {"chat": "42", "stop": true}, {"chat": "42", "reset": true}, {"chat": "42", "status": 500, "rejectSocket": true, "restore": true}, {"chat": "42", "status": 200, "rejectSocket": false, "restore": true}, {"chat": "42", "reset": true, "permission": "bypassPermissions", "restore": true}, {"chat": "42", "restore": true}, {"chat": "42", "seed": seed, "permission": "default", "status": 404, "restore": true}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-im-recovery-bridge.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IM_RECOVERY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"work": work, "seed": seed, "actions": actions}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var want any
	if err = json.Unmarshal(out, &want); err != nil {
		t.Fatal(err, string(out))
	}
	private := t.TempDir()
	store := NewSessionStore(private)
	if err = os.WriteFile(store.Path, raw(seed), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	status, permission, reject := 200, "default", false
	requests, messages := []any{}, []any{}
	socket := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		id := strings.TrimPrefix(conn.Request().URL.Path, "/ws/")
		for {
			var message json.RawMessage
			if err := websocket.JSON.Receive(conn, &message); err != nil {
				return
			}
			mu.Lock()
			messages = append(messages, map[string]any{"id": id, "message": object(message)})
			mu.Unlock()
		}
	})}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			requests = append(requests, map[string]any{"path": r.URL.Path, "token": r.URL.Query().Get("token")})
			denied := reject
			mu.Unlock()
			if denied {
				w.WriteHeader(403)
				return
			}
			socket.ServeHTTP(w, r)
			return
		}
		requests = append(requests, map[string]any{"path": r.URL.Path, "authorization": r.Header.Get("Authorization")})
		s, p := status, permission
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s)
		_, _ = w.Write(raw(map[string]string{"workDir": work, "permissionMode": p, "message": "private unavailable"}))
	}))
	defer host.Close()
	config := Config{Home: home, Roots: []string{home}, ServerURL: "ws" + strings.TrimPrefix(host.URL, "http"), Environment: map[string]string{"CC_HAHA_LOCAL_ACCESS_TOKEN": "private-local-token"}}
	bridge := NewBridge(ctx, config, nil, func(string) {})
	defer bridge.Close()
	client := NewHTTPClient(config, host.Client())
	rows := []any{}
	clears := 0
	for _, action := range actions {
		chat := action["chat"].(string)
		if seed, ok := action["seed"]; ok {
			if err = os.WriteFile(store.Path, raw(seed), 0600); err != nil {
				t.Fatal(err)
			}
		}
		mu.Lock()
		if s, ok := action["status"]; ok {
			status = s.(int)
		}
		if p, ok := action["permission"]; ok {
			permission = p.(string)
		}
		if r, ok := action["rejectSocket"]; ok {
			reject = r.(bool)
		}
		mu.Unlock()
		if action["reset"] == true {
			bridge.Reset(chat)
		}
		if action["restore"] == true {
			result, err := RestoreSession(ctx, chat, store, client, bridge, func(context.Context, protocol.JSONObject) error { return nil }, func() { clears++ })
			if err != nil {
				t.Fatal(err)
			}
			value := map[string]any{"status": result.Status}
			if result.Session != nil {
				value["session"] = result.Session
			}
			rows = append(rows, map[string]any{"result": value, "clears": clears, "stored": store.Get(chat)})
		}
		send := false
		switch {
		case action["send"] == true:
			send = bridge.SendUser(chat, "<actual incoming>", nil)
		case action["allow"] == true:
			send = bridge.Send(chat, object([]byte(`{"type":"permission_response","requestId":"private-request","allowed":true,"rule":"always"}`)))
		case action["stop"] == true:
			send = bridge.Send(chat, object([]byte(`{"type":"stop_generation"}`)))
		default:
			continue
		}
		rows = append(rows, map[string]any{"sent": send})
		if send {
			end := time.Now().Add(time.Second)
			for {
				mu.Lock()
				n := len(messages)
				mu.Unlock()
				expected := 1
				if action["allow"] == true {
					expected = 2
				}
				if action["stop"] == true {
					expected = 3
				}
				if n >= expected {
					break
				}
				if time.Now().After(end) {
					t.Fatal("physical WS message missing")
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	bridge.Close()
	mu.Lock()
	got := raw(map[string]any{"rows": rows, "requests": requests, "messages": messages})
	mu.Unlock()
	var actual any
	_ = json.Unmarshal(got, &actual)
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("actual HTTP/WS recovery %s want%s", got, out)
	}
}
