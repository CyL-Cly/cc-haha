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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// The CLI fast path launches its ordinary inner session from the already-created
// worktree. A later desktop process must restore that same history and directory
// with the default catalog, rather than relying on desktop repository creation.
func TestDesktopColdResumeOrdinaryCLIWorktreeDefaultCatalog(t *testing.T) {
	var calls atomic.Int32
	var filename string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round := calls.Add(1)
		switch round {
		case 1, 3:
			if round == 3 && !bytes.Contains(body, []byte("CLI WORKTREE COMPLETE")) {
				t.Error("desktop request lost ordinary CLI history")
			}
			response(w, "cold-worktree-read-"+strconv.Itoa(int(round)), "Read", map[string]any{"file_path": filename}, "")
		case 2, 4:
			if !bytes.Contains(body, []byte("PRIVATE COLD WORKTREE BYTES")) {
				t.Error("actual Read result absent from next request")
			}
			text := "CLI WORKTREE COMPLETE"
			if round == 4 {
				text = "DESKTOP WORKTREE COMPLETE"
			}
			response(w, "cold-worktree-finished-"+strconv.Itoa(int(round)), "", nil, text)
		default:
			t.Error("unexpected model request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.WorkDir, _ = filepath.EvalSymlinks(o.WorkDir)
	o.Native = nil
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(filepath.Dir(o.WorkDir), "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Arguments.PermissionMode = "default"
	for k, v := range map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(o.Environment["HOME"], "absent-gitconfig"), "GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"} {
		o.Environment[k] = v
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, "tracked.txt"), []byte("PRIVATE COLD WORKTREE BYTES\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "private fixture"}} {
		result, err := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: o.WorkDir, Environment: o.Environment, IgnoreStdin: true})
		if err != nil || result.Code != 0 {
			t.Fatal(result, err)
		}
	}
	name := "cold-ordinary-cli"
	o.Arguments.Worktree = &worktree.Startup{Name: &name}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close(context.Background()) })
	filename = filepath.Join(o.WorkDir, ".claude", "worktrees", name, "tracked.txt")
	if err = cli.Submit(ctx, tui.Input{Text: "Read the actual tracked file."}, nil); err != nil {
		t.Fatal(err)
	}
	sid := cli.process.Controller.SessionID()
	if err = cli.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o.Arguments.Worktree = nil
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: sid, WorkDir: filepath.Dir(filename), Resume: true}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Read that same worktree file again."})); err != nil {
		t.Fatal(err)
	}
	desktop := live.(*DesktopSession)
	cwd, err := desktop.currentEngine().process.WorkingDirectory(ctx)
	if err != nil || cwd != filepath.Dir(filename) {
		t.Fatal("desktop did not restore actual CLI worktree", cwd, err)
	}
	if calls.Load() != 4 {
		t.Fatal("missing CLI/desktop tool loop", calls.Load())
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "PRIVATE COLD WORKTREE BYTES\n" {
		t.Fatal("tracked file changed", string(data), err)
	}
}
