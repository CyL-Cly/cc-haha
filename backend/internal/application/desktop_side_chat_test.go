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

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func TestActualDesktopSideChatSharesHarnessAtStableBoundaryWithoutDurableHistory(t *testing.T) {
	const initial = "SIDE_PARENT_REFERENCE_18BC"
	const later = "PARENT_LATER_MUST_NOT_ENTER_SIDE_390E"
	const marker = "SIDE_EXPLICIT_WORKSPACE_WRITE_3C6D\n"
	var calls atomic.Int32
	var outputPath string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		switch calls.Add(1) {
		case 1:
			response(w, "side-parent-initial", "", nil, initial)
		case 2:
			response(w, "side-parent-later", "", nil, later)
		case 3:
			if !bytes.Contains(body, []byte(initial)) || bytes.Contains(body, []byte(later)) || !bytes.Contains(body, []byte("All inherited history from the parent conversation is reference context only")) {
				t.Error("Side request did not use captured parent boundary + original side system prompt", string(body))
			}
			response(w, "side-write", "Write", map[string]any{"file_path": outputPath, "content": marker}, "")
		case 4:
			response(w, "side-read", "Read", map[string]any{"file_path": outputPath}, "")
		case 5:
			if !bytes.Contains(body, []byte(strings.TrimSpace(marker))) {
				t.Error("Real side tool result did not return to shared model loop")
			}
			response(w, "side-final", "", nil, "SIDE_ACTUAL_SHARED_OK")
		case 6:
			if !bytes.Contains(body, []byte("SIDE_ACTUAL_SHARED_OK")) {
				t.Error("Live side conversation lost its own prior turn")
			}
			if obj(body).String("model") != "fixture-model-next" {
				t.Error("Same-provider selection did not reach the existing model consumer", obj(body).String("model"))
			}
			response(w, "side-second", "", nil, "SIDE_SECOND_TURN_OK")
		default:
			t.Error("Unexpected model request")
			http.Error(w, "extra", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = nil
	outputPath = filepath.Join(o.WorkDir, "side-output.txt")
	committed := make(chan string, 128)
	factory := DesktopFactory(o)
	s, e := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: func(ctx context.Context, options server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
		original := options.OnCanonicalTranscriptMutation
		options.OnCanonicalTranscriptMutation = func(path string) error {
			if original != nil {
				if err := original(path); err != nil {
					return err
				}
			}
			select {
			case committed <- path:
			default:
			}
			return nil
		}
		return factory(ctx, options, emit)
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	// As in the desktop fork regression, idle does not join Source's
	// independent Record promise. Observe actual file-close commits before
	// capturing history or comparing durable bytes, without forcing a flush.
	readCommitted := func(id, marker string) (string, []byte) {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case path := <-committed:
				if filepath.Base(path) != id+".jsonl" {
					continue
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(data, []byte(marker)) {
					return path, data
				}
			case <-deadline.C:
				t.Fatalf("canonical write did not publish %q", marker)
			}
		}
	}
	post := func(path string, body any, status int) json.RawMessage {
		t.Helper()
		res, e := host.Client().Post(host.URL+path, "application/json", bytes.NewReader(raw(body)))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatal(res.StatusCode, string(data))
		}
		return data
	}
	parent := obj(post("/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201)).String("sessionId")
	connect := func(id string) *websocket.Conn {
		t.Helper()
		ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
		if e != nil {
			t.Fatal(e)
		}
		ws.SetDeadline(time.Now().Add(20 * time.Second))
		return ws
	}
	turn := func(ws *websocket.Conn, prompt string) {
		t.Helper()
		if e := websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": prompt}))); e != nil {
			t.Fatal(e)
		}
		complete := false
		for {
			var text string
			if e := websocket.Message.Receive(ws, &text); e != nil {
				t.Fatal(e)
			}
			v := obj([]byte(text))
			if v.String("type") == "error" {
				t.Fatal(text)
			}
			if v.String("type") == "session_state" && v.String("turnState") == "running" {
				t.Fatal("New user admission falsely replayed a reconnect snapshot", text)
			}
			if v.String("type") == "message_complete" {
				complete = true
				requestDesktopSessionSync(t, ws)
			}
			if complete && v.String("type") == "session_state" {
				assertDesktopIdleSnapshot(t, v)
				return
			}
		}
	}
	parentWS := connect(parent)
	defer parentWS.Close()
	turn(parentWS, "Prepare reference context")
	parentPath, _ := readCommitted(parent, initial)
	cliID := "22222222-2222-4222-8222-222222222222"
	side := obj(post("/api/sessions/"+parent+"/side-chats", map[string]any{"sideChatId": cliID}, 200)).String("sessionId")
	if side != "side-"+cliID {
		t.Fatal(side)
	}
	turn(parentWS, "Append parent later context")
	laterPath, before := readCommitted(parent, later)
	if laterPath != parentPath {
		t.Fatal("parent canonical path changed between committed turns", parentPath, laterPath)
	}
	sideWS := connect(side)
	defer sideWS.Close()
	turn(sideWS, "This side conversation explicitly requests Write of the private result and then Read to verify it.")
	control := func(message any, kind, code string) {
		t.Helper()
		if e := websocket.Message.Send(sideWS, string(raw(message))); e != nil {
			t.Fatal(e)
		}
		for {
			var text string
			if e := websocket.Message.Receive(sideWS, &text); e != nil {
				t.Fatal(e)
			}
			v := obj([]byte(text))
			if v.String("type") == kind && (code == "" || v.String("code") == code) {
				return
			}
			if v.String("type") == "error" {
				t.Fatal(text)
			}
		}
	}
	control(map[string]any{"type": "set_runtime_config", "providerId": nil, "modelId": "fixture-model-next"}, "runtime_config_applied", "")
	for _, id := range []string{"different-provider", "openai-official"} {
		control(map[string]any{"type": "set_runtime_config", "providerId": id, "modelId": "must-not-apply"}, "error", "SIDE_CHAT_RUNTIME_RESTART_UNAVAILABLE")
	}
	if calls.Load() != 5 {
		t.Fatal("Runtime controls made an unexpected model request", calls.Load())
	}
	// Reconnect to the same running ephemeral host, rather than rebuilding a
	// second conversation from the parent transcript.
	_ = sideWS.Close()
	sideWS = connect(side)
	defer sideWS.Close()
	turn(sideWS, "Continue this side chat only")
	if calls.Load() != 6 {
		t.Fatal(calls.Load())
	}
	output, e := os.ReadFile(outputPath)
	if e != nil || string(output) != marker {
		t.Fatal("No actual side Write/Read", e, string(output))
	}
	after, e := os.ReadFile(parentPath)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Side conversation changed parent canonical", e)
	}
	_ = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && (filepath.Base(path) == cliID+".jsonl" || filepath.Base(path) == side+".jsonl") {
			t.Error("Temporary side wrote durable canonical", path)
		}
		return e
	})
	res, e := host.Client().Get(host.URL + "/api/sessions/" + side + "/messages?mode=full")
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(data, []byte(`"sourceVersion":"ephemeral"`)) || !bytes.Contains(data, []byte(`"messages":[]`)) {
		t.Fatal(res.StatusCode, string(data))
	}
	req, _ := http.NewRequest("DELETE", host.URL+"/api/sessions/"+parent+"/side-chats/"+side, nil)
	res, e = host.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(data, []byte(`"closed":true`)) {
		t.Fatal(res.StatusCode, string(data))
	}
	res, e = host.Client().Get(host.URL + "/api/sessions/" + side + "/summary")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("Closed temporary side is still available", res.StatusCode)
	}
}
