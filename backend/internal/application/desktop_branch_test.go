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

func TestActualDesktopForkResumesSameCanonicalHarnessThenWritesAndReads(t *testing.T) {
	const contextMarker = "CANONICAL_PRE_FORK_CONTEXT_238A"
	const outputMarker = "ACTUAL_FORK_WRITE_READ_C914\n"
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
			response(w, "before-fork", "", nil, contextMarker)
		case 2:
			if !bytes.Contains(body, []byte(contextMarker)) || !bytes.Contains(body, []byte("Continue after fork")) {
				t.Error("fork did not resume original context in real request", string(body))
			}
			response(w, "fork-write", "Write", map[string]any{"file_path": outputPath, "content": outputMarker}, "")
		case 3:
			data, e := os.ReadFile(outputPath)
			if e != nil || string(data) != outputMarker {
				t.Error("actual fork Write did not execute", e)
			}
			response(w, "fork-read", "Read", map[string]any{"file_path": outputPath}, "")
		case 4:
			if !bytes.Contains(body, []byte(strings.TrimSpace(outputMarker))) {
				t.Error("actual fork Read did not return to original query loop")
			}
			response(w, "fork-final", "", nil, "ACTUAL_FORK_E2E_OK")
		default:
			t.Error("unexpected extra model loop")
			http.Error(w, "extra call", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = nil
	outputPath = filepath.Join(o.WorkDir, "fork-output.txt")
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
	// Source's independent Record promise is not joined by an idle snapshot.
	// Observe actual canonical write commits before asserting saved bytes; no
	// flush, ordering override or fixed delay is introduced into the Harness.
	readCommitted := func(path, marker string) []byte {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			data, err := os.ReadFile(path)
			if err == nil && bytes.Contains(data, []byte(marker)) {
				return data
			}
			select {
			case <-committed:
			case <-deadline.C:
				t.Fatalf("canonical write did not publish %q: %v %s", marker, err, data)
			}
		}
	}
	post := func(path string, body any, expected int) json.RawMessage {
		t.Helper()
		res, e := host.Client().Post(host.URL+path, "application/json", bytes.NewReader(raw(body)))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != expected {
			t.Fatal(res.StatusCode, e, string(data))
		}
		return data
	}
	id := obj(post("/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201)).String("sessionId")
	turn := func(id, prompt string) {
		t.Helper()
		ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
		if e != nil {
			t.Fatal(e)
		}
		defer ws.Close()
		ws.SetDeadline(time.Now().Add(15 * time.Second))
		if e = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": prompt}))); e != nil {
			t.Fatal(e)
		}
		completed := false
		for {
			var frame string
			if e = websocket.Message.Receive(ws, &frame); e != nil {
				t.Fatal(e)
			}
			v := obj([]byte(frame))
			if v.String("type") == "error" {
				t.Fatal(frame)
			}
			if v.String("type") == "message_complete" {
				completed = true
				requestDesktopSessionSync(t, ws)
			}
			if completed && v.String("type") == "session_state" {
				assertDesktopIdleSnapshot(t, v)
				return
			}
		}
	}
	turn(id, "Prepare canonical fork context.")
	var path string
	e = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && filepath.Base(p) == id+".jsonl" {
			path = p
		}
		return nil
	})
	if e != nil || path == "" {
		t.Fatal(e, path)
	}
	before := readCommitted(path, contextMarker)
	target := ""
	for _, line := range bytes.Split(before, []byte{'\n'}) {
		v := obj(line)
		if v.String("type") == "assistant" && bytes.Contains(line, []byte(contextMarker)) {
			target = v.String("uuid")
		}
	}
	if target == "" {
		t.Fatal("canonical assistant UUID absent", string(before))
	}
	fork := obj(post("/api/sessions/"+id+"/branch", map[string]any{"targetMessageId": target, "title": "Actual harness fork"}, 201)).String("sessionId")
	if fork == "" || fork == id {
		t.Fatal(fork)
	}
	turn(fork, "Continue after fork. Write then read the physical result.")
	if calls.Load() != 4 {
		t.Fatal("unexpected physical requests", calls.Load())
	}
	unchanged, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, unchanged) {
		t.Fatal("fork continuation changed original transcript", e)
	}
	data := readCommitted(filepath.Join(filepath.Dir(path), fork+".jsonl"), "ACTUAL_FORK_E2E_OK")
	for _, want := range []string{contextMarker, "call-fork-write", "call-fork-read", "ACTUAL_FORK_E2E_OK", `"forkedFrom"`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatal("new canonical history lost", want, string(data))
		}
	}
}
