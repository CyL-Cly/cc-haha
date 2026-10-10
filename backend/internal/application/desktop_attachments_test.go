package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func TestActualDesktopAttachmentWebSocketStagesFileAndSharedHarnessReadsIt(t *testing.T) {
	const marker = "ACTUAL_DESKTOP_ATTACHMENT_PHYSICAL_D831\n"
	var calls atomic.Int32
	var stagedPath string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if calls.Add(1) == 1 {
			// Original @file expansion already reads the staged file into this
			// first request. An explicit Read consequently returns file_unchanged.
			if !bytes.Contains(body, []byte(strings.TrimSpace(marker))) {
				t.Error("original attachment expansion did not read staged bytes")
			}
			match := regexp.MustCompile(`@\\"([^"\\]+)\\"`).FindSubmatch(body)
			if len(match) != 2 || !bytes.Contains(body, []byte(`Read the attached file`)) {
				t.Errorf("actual model did not receive original attachment content blocks: %s", body)
				http.Error(w, "invalid attachment", 500)
				return
			}
			stagedPath = string(match[1])
			data, err := os.ReadFile(stagedPath)
			if err != nil || string(data) != marker {
				t.Error("actual staged bytes", err, string(data))
			}
			response(w, "desktop-att-read", "Read", map[string]any{"file_path": stagedPath}, "")
			return
		}
		if !bytes.Contains(body, []byte(strings.TrimSpace(marker))) {
			t.Error("real Read result did not return to the same shared Harness")
		}
		if !bytes.Contains(body, []byte("File unchanged since last read")) {
			t.Error("attachment expansion and Read did not share the actual read cache")
		}
		response(w, "desktop-att-result", "", nil, "DESKTOP_ATTACHMENT_E2E_OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	res, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]string{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	id := obj(data).String("sessionId")
	if res.StatusCode != 201 || id == "" {
		t.Fatal(res.StatusCode, string(data))
	}
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
	if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "\ufeff  Read the attached file  \u00a0", "attachments": []any{map[string]any{"type": "file", "name": "中文👋.txt", "data": "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(marker))}}}))); err != nil {
		t.Fatal(err)
	}
	completed := false
	for {
		var frame string
		if err = websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatal(err)
		}
		value := obj([]byte(frame))
		if value.String("type") == "error" {
			t.Fatal("actual desktop attachment failed", frame)
		}
		if value.String("type") == "message_complete" {
			completed = true
		}
		if completed && value.String("type") == "session_state" && value.String("turnState") == "idle" {
			break
		}
	}
	if calls.Load() != 2 || stagedPath == "" || !strings.HasPrefix(stagedPath, filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "uploads", id)+string(filepath.Separator)) {
		t.Fatal("attachment escaped session staging or started another model loop", calls.Load(), stagedPath)
	}
	var transcript []byte
	if err = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() && filepath.Base(path) == id+".jsonl" {
			transcript, e = os.ReadFile(path)
			return e
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{stagedPath, "call-desktop-att-read", "file_unchanged", "DESKTOP_ATTACHMENT_E2E_OK"} {
		if !bytes.Contains(transcript, []byte(expected)) {
			t.Fatal("canonical transcript lost attachment/tool/result", expected)
		}
	}
}
