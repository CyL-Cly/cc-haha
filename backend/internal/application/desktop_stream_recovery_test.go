package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func TestActualDesktopStreamRetryThenApprovalToolsAndNextHuman(t *testing.T) {
	var posts atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch n := posts.Add(1); n {
		case 1:
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"private failed stream\"}}\n\n")
		case 2:
			response(w, "desktop-retry-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_DESKTOP_RECOVERY"}, "")
		case 3, 5:
			if !bytes.Contains(body, []byte("desktop-retry-write")) {
				t.Error("actual history lost after retry")
			}
			response(w, fmt.Sprintf("desktop-retry-read-%d", n), "Read", map[string]any{"file_path": target}, "")
		case 4, 6:
			if !bytes.Contains(body, []byte("ACTUAL_DESKTOP_RECOVERY")) {
				t.Error("real tool result missing")
			}
			response(w, fmt.Sprintf("desktop-retry-done-%d", n), "", nil, "DESKTOP RECOVERY DONE")
		default:
			t.Error("unexpected physical model request", n)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "default"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "2"
		env["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
	}
	target = filepath.Join(o.WorkDir, "desktop-recovery.txt")
	s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s)
	defer h.Close()
	defer s.Close(context.Background())
	r, err := h.Client().Post(h.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "default"})))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 201 {
		t.Fatalf("create %d %s", r.StatusCode, data)
	}
	id := obj(data).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+id, "", h.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	recoveries, completed, reads := 0, 0, 0
	send := func(v any) {
		t.Helper()
		if e := websocket.Message.Send(ws, string(raw(v))); e != nil {
			t.Fatal(e)
		}
	}
	for turn := 0; turn < 2; turn++ {
		if turn == 0 {
			var connected string
			if err := websocket.Message.Receive(ws, &connected); err != nil {
				t.Fatal(err)
			}
		}
		send(map[string]any{"type": "user_message", "content": []string{"Write and read the artifact.", "Read the same artifact again."}[turn]})
		for steps := 0; steps < 100; steps++ {
			var line string
			if err := websocket.Message.Receive(ws, &line); err != nil {
				t.Fatal(err)
			}
			m := obj([]byte(line))
			switch m.String("type") {
			case "error":
				t.Fatal("actual desktop error", line)
			case "streaming_fallback":
				recoveries++
				if m.String("cause") != "stream_retry" {
					t.Fatal(line)
				}
			case "permission_request":
				send(map[string]any{"type": "permission_response", "requestId": m.String("requestId"), "allowed": true})
			case "tool_result":
				if strings.HasPrefix(m.String("toolUseId"), "call-desktop-retry-read-") {
					reads++
				}
			case "message_complete":
				completed++
				requestDesktopSessionSync(t, ws)
			case "session_state":
				if completed == turn+1 {
					assertDesktopIdleSnapshot(t, m)
					goto nextTurn
				}
			}
		}
		t.Fatal("desktop turn did not finish")
	nextTurn:
	}
	if posts.Load() != 6 || recoveries != 1 || completed != 2 || reads < 2 {
		t.Fatalf("posts%d recoveries%d completed%d reads%d", posts.Load(), recoveries, completed, reads)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "ACTUAL_DESKTOP_RECOVERY" {
		t.Fatal(err, string(content))
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	err = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && strings.HasSuffix(path, id+".jsonl") {
			found = true
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if !bytes.Contains(b, []byte("ACTUAL_DESKTOP_RECOVERY")) {
				t.Fatal("real successful artifact absent from canonical history")
			}
			// Actual MAIN QueryEngine keeps retry/recovery controls in mutable
			// history. The next ASK persists them once; failed-attempt assistant
			// output remains absent. See the physical Source consumer regression
			// TestNativeRetryAndRecoveryActualQueryEngineOutputAndNextASKPersistence.
			retries, recoveries := 0, 0
			for _, line := range bytes.Split(b, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var record protocol.JSONObject
				if e := json.Unmarshal(line, &record); e != nil {
					return e
				}
				if record.String("type") == "assistant" && bytes.Contains(line, []byte("private failed stream")) {
					t.Fatal("failed model attempt entered assistant canonical history")
				}
				if record.String("type") == "system" {
					switch record.String("subtype") {
					case "api_error":
						retries++
					case "streaming_fallback":
						recoveries++
						if record.String("cause") != "stream_retry" || record.String("uuid") == "" {
							t.Fatal("Source recovery identity/cause lost", string(line))
						}
					}
				}
			}
			if retries != 1 || recoveries != 1 {
				t.Fatal("Source next-ASK system controls not persisted once", retries, recoveries)
			}
		}
		return walkErr
	})
	if err != nil || !found {
		t.Fatal("physical canonical", found, err)
	}
}
