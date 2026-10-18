package application

import (
	"bytes"
	"context"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"golang.org/x/net/websocket"
)

func TestDesktopRepositoryPlanStartsGoWorktreeArtifactsAndResumes(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "repository-write", "Write", map[string]any{"file_path": "desktop-worktree-proof.txt", "content": "WORKTREE_DESKTOP_E2E"}, "")
		case 2:
			response(w, "repository-read", "Read", map[string]any{"file_path": "desktop-worktree-proof.txt"}, "")
		case 3:
			if !bytes.Contains(body, []byte("WORKTREE_DESKTOP_E2E")) {
				t.Error("actual native Read missing")
			}
			response(w, "repository-complete", "", nil, "WORKTREE DESKTOP COMPLETE")
		case 4:
			if !bytes.Contains(body, []byte("WORKTREE DESKTOP COMPLETE")) {
				t.Error("actual resumed history lost")
			}
			response(w, "repository-resumed", "", nil, "WORKTREE DESKTOP RESUMED")
		default:
			t.Errorf("unexpected model call %d", calls.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.WorkDir, _ = filepath.EvalSymlinks(o.WorkDir)
	o.Native = nil
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(filepath.Dir(o.WorkDir), "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}
	env := o.Environment
	for k, v := range map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(env["HOME"], "absent-gitconfig"), "GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"} {
		env[k] = v
	}
	_ = os.WriteFile(filepath.Join(o.WorkDir, "tracked.txt"), []byte("repository tracked"), 0600)
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "tracked.txt"}, {"commit", "-m", "private initial"}, {"branch", "feature"}} {
		result, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: o.WorkDir, Environment: env})
		if e != nil || result.Code != 0 {
			t.Fatal(result, e)
		}
	}
	options := server.Options{HomeDir: env["HOME"], ConfigDir: env["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Environment: func() map[string]string { return env }, Factory: DesktopFactory(o)}
	start := func() (*server.Server, *httptest.Server) {
		s, e := server.New(options)
		if e != nil {
			t.Fatal(e)
		}
		h := httptest.NewServer(s)
		t.Cleanup(func() { _ = s.Close(context.Background()); h.Close() })
		return s, h
	}
	s, host := start()
	request, e := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "repository": map[string]any{"branch": "feature", "worktree": true}, "permissionMode": "bypassPermissions"})))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Content-Type", "application/json")
	created, e := host.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	if created.StatusCode != 201 {
		t.Fatal("create", created.StatusCode, string(data))
	}
	id := obj(data).String("sessionId")
	physical := filepath.Join(o.WorkDir, ".claude", "worktrees", "desktop-feature-"+id[:8])
	if _, e = os.Stat(physical); !os.IsNotExist(e) {
		t.Fatal("new session prematurely materialized worktree", e)
	}
	original := filepath.Join(env["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.WorkDir), id+".jsonl")
	planned, e := os.ReadFile(original)
	if e != nil || !bytes.Contains(planned, []byte(`"worktreeSlug":"desktop-feature-`+id[:8])) {
		t.Fatal("persisted repository plan", e, string(planned))
	}
	dial := func(h *httptest.Server) *websocket.Conn {
		ws, e := websocket.Dial("ws"+strings.TrimPrefix(h.URL, "http")+"/ws/"+id, "", h.URL)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = ws.Close() })
		_ = ws.SetDeadline(time.Now().Add(10 * time.Second))
		return ws
	}
	receive := func(ws *websocket.Conn, kind string) {
		for i := 0; i < 200; i++ {
			var frame string
			if e := websocket.Message.Receive(ws, &frame); e != nil {
				t.Fatal(e)
			}
			value := obj([]byte(frame))
			if value.String("type") == "error" {
				t.Fatal("desktop actual runtime", frame)
			}
			if value.String("type") == kind {
				return
			}
		}
		t.Fatal("missing frame", kind)
	}
	ws := dial(host)
	receive(ws, "connected")
	if e = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Write and read the worktree proof."}))); e != nil {
		t.Fatal(e)
	}
	receive(ws, "message_complete")
	requestDesktopSessionSync(t, ws)
	for {
		var frame string
		if e = websocket.Message.Receive(ws, &frame); e != nil {
			t.Fatal(e)
		}
		value := obj([]byte(frame))
		if value.String("type") == "session_state" {
			assertDesktopIdleSnapshot(t, value)
			break
		}
	}
	_ = ws.Close()
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	host.Close()
	proof, e := os.ReadFile(filepath.Join(physical, "desktop-worktree-proof.txt"))
	if e != nil || string(proof) != "WORKTREE_DESKTOP_E2E" {
		t.Fatal("physical actual worktree artifact", string(proof), e)
	}
	if _, e = os.Stat(filepath.Join(o.WorkDir, "desktop-worktree-proof.txt")); !os.IsNotExist(e) {
		t.Fatal("artifact leaked to initial repo", e)
	}
	if _, e = os.Stat(original); !os.IsNotExist(e) {
		t.Fatal("stale repository placeholder remains", e)
	}
	files, e := filepath.Glob(filepath.Join(env["CLAUDE_CONFIG_DIR"], "projects", "*", id+".jsonl"))
	if e != nil || len(files) != 1 {
		t.Fatal("multiple canonical transcripts", files, e)
	}
	transcript, e := os.ReadFile(files[0])
	if e != nil || !bytes.Contains(transcript, []byte(`"type":"worktree-state"`)) || !bytes.Contains(transcript, []byte(`"worktreePath":"`+physical)) {
		t.Fatal("resume worktree state absent", e, string(transcript))
	}
	_, host = start()
	ws = dial(host)
	receive(ws, "connected")
	if e = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Continue the saved worktree session."}))); e != nil {
		t.Fatal(e)
	}
	receive(ws, "message_complete")
	if calls.Load() != 4 {
		t.Fatal("actual model requests", calls.Load())
	}
}
