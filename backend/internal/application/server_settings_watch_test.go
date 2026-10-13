package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopFactoryExternalSettingsWatcherChangesSameSessionAndCloses(t *testing.T) {
	var calls, opens atomic.Int32
	var target string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "watch-write", "Write", map[string]any{"file_path": target, "content": "DESKTOP_WATCH_ORIGINAL"}, "")
		case 2:
			response(w, "watch-read", "Read", map[string]any{"file_path": target}, "")
		case 3:
			if !bytes.Contains(body, []byte("DESKTOP_WATCH_ORIGINAL")) {
				t.Error("actual physical Write/Read missing")
			}
			response(w, "watch-first-done", "", nil, "WATCH_FIRST_DONE")
		case 4:
			response(w, "watch-denied", "Write", map[string]any{"file_path": target, "content": "must-not-replace"}, "")
		case 5:
			found := false
			for _, msg := range taskSourceBlocks(obj(body).Get("messages")) {
				for _, block := range taskSourceBlocks(msg.Get("content")) {
					if block.String("tool_use_id") == "call-watch-denied" && string(block.Get("is_error")) == "true" {
						found = true
					}
				}
			}
			if !found {
				t.Error("Source disk deny absent from actual next POST")
			}
			response(w, "watch-second-done", "", nil, "WATCH_DENY_DONE")
		default:
			t.Error("watcher started an extra model request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "bypassPermissions"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	target = filepath.Join(o.WorkDir, "desktop-watch-result.txt")
	hookLog := filepath.Join(o.WorkDir, "desktop-watch-hooks.log")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"ConfigChange": []any{map[string]any{"matcher": "user_settings", "hooks": []any{map[string]any{"type": "command", "command": "printf 'called\\n' >> " + quote(hookLog) + "; printf '{}'"}}}}}, "future": map[string]any{"keep": true}}
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "settings.json")
	if err := os.WriteFile(path, raw(settings), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		opens.Add(1)
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	req, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer fake-only-loopback")
	resp, err := host.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("actual create %d %s", resp.StatusCode, body)
	}
	id := obj(body).String("sessionId")
	wsConfig, err := websocket.NewConfig("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, host.URL)
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header.Set("Authorization", "Bearer fake-only-loopback")
	ws, err := websocket.DialConfig(wsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(25 * time.Second))
	turn := func(text string) {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": text}))); err != nil {
			t.Fatal(err)
		}
		complete := false
		for i := 0; i < 150; i++ {
			var frame string
			if err := websocket.Message.Receive(ws, &frame); err != nil {
				t.Fatal(err)
			}
			message := obj([]byte(frame))
			if message.String("type") == "error" {
				t.Fatalf("actual factory %s", frame)
			}
			if message.String("type") == "message_complete" {
				complete = true
				requestDesktopSessionSync(t, ws)
			}
			if complete && message.String("type") == "session_state" {
				assertDesktopIdleSnapshot(t, message)
				return
			}
		}
		t.Fatal("actual factory did not settle")
	}
	turn("Write and Read actual desktop result")
	opened := opens.Load()
	if calls.Load() != 3 {
		t.Fatal("unexpected initial request count")
	}
	settings["permissions"] = map[string]any{"deny": []string{"Write"}}
	if err := os.WriteFile(path, raw(settings), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		data, _ := os.ReadFile(hookLog)
		if strings.Contains(string(data), "called") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("desktop native ConfigChange Hook never executed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The native Hook writes this marker before its process result resolves.
	// Await its bounded local settlement; the next actual tool call proves the
	// permission consumer itself instead of assuming the marker means applied.
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 3 || opens.Load() != opened {
		t.Fatal("settings change opened another Agent or model query")
	}
	turn("Try Write after external settings change")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "DESKTOP_WATCH_ORIGINAL" {
		t.Fatal("actual desktop deny failed to preserve physical result")
	}
	if calls.Load() != 5 || opens.Load() != opened {
		t.Fatal("settings reload did not reuse actual desktop Harness")
	}
	_ = ws.Close()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(hookLog)
	settings["future"] = map[string]any{"keep": true, "afterClose": true}
	if err = os.WriteFile(path, raw(settings), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1700 * time.Millisecond)
	after, _ := os.ReadFile(hookLog)
	if !bytes.Equal(before, after) || calls.Load() != 5 {
		t.Fatal("desktop watcher/native Hook survived closed service")
	}
}

func taskSourceBlocks(rawData []byte) []protocol.JSONObject {
	var value []protocol.JSONObject
	_ = json.Unmarshal(rawData, &value)
	return value
}
