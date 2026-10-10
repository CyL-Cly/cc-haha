package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func privateTmuxFastFixture(t *testing.T) (nativePluginFixture, string, func(...string) worktree.ProcessResult) {
	t.Helper()
	tmux, e := exec.LookPath("tmux")
	if e != nil {
		t.Skip("actual tmux executable required")
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["TMPDIR"] = f.n.Temp
	f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
	f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "absent.gitconfig")
	// Unix sockets have a fixed path limit independent of the worktree path.
	// A quality lane's nested private HOME may already exceed that limit.
	socketRoot, e := os.MkdirTemp("/tmp", "wt-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	socket := filepath.Join(socketRoot, "tmux.socket")
	bindir := filepath.Join(f.home, "bin")
	f.write(t, filepath.Join(bindir, "tmux"), "#!/bin/sh\nexec "+shellTmuxTest(tmux)+" -S "+shellTmuxTest(socket)+" -f /dev/null \"$@\"\n")
	if e = os.Chmod(filepath.Join(bindir, "tmux"), 0700); e != nil {
		t.Fatal(e)
	}
	f.n.Environment["PATH"] = bindir + ":/usr/bin:/bin"
	f.n.Environment["TMUX"] = "owned-unattached-socket,1,0"
	run := func(args ...string) worktree.ProcessResult {
		t.Helper()
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: tmux, Arguments: append([]string{"-S", socket, "-f", "/dev/null"}, args...), CWD: f.cwd, Environment: f.n.Environment})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	t.Cleanup(func() { run("kill-server") })
	return f, socket, run
}
func shellTmuxTest(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }

func TestNativeTmuxFastPathCommandHTTPHooksAndErrorsUseActualOutsideOwner(t *testing.T) {
	for _, kind := range []string{"command", "http", "http-sandbox", "empty-matcher", "failure", "simple"} {
		t.Run(kind, func(t *testing.T) {
			f, _, run := privateTmuxFastFixture(t)
			hookDir := filepath.Join(f.home, "hook-worktree")
			if e := os.MkdirAll(hookDir, 0700); e != nil {
				t.Fatal(e)
			}
			marker := filepath.Join(f.home, "hook-input.json")
			hook := map[string]any{"type": "command", "command": "cat > " + shellTmuxTest(marker) + "; printf '%s\\n' " + shellTmuxTest(hookDir)}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				calls++
				if e = os.WriteFile(marker, body, 0600); e != nil {
					t.Error(e)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": hookDir}})
			}))
			defer srv.Close()
			if kind == "http" || kind == "http-sandbox" {
				hook = map[string]any{"type": "http", "url": srv.URL}
			}
			if kind == "failure" {
				hook["command"] = "printf 'actual native failure' >&2; exit 2"
			}
			hooks := []any{hook}
			if kind == "empty-matcher" {
				hooks = []any{}
			}
			config := map[string]any{"hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": hooks}}}}
			if kind == "http-sandbox" {
				config["sandbox"] = map[string]any{"enabled": true, "network": map[string]any{"allowedDomains": []string{}, "httpProxyPort": 1}}
			}
			settings, _ := json.Marshal(config)
			f.write(t, filepath.Join(f.config, "settings.json"), string(settings))
			if kind == "simple" {
				f.n.Environment["CLAUDE_CODE_SIMPLE"] = "true"
			}
			var out, stderr bytes.Buffer
			result, e := RunNativeTmuxWorktree(context.Background(), []string{"--tmux", "--worktree=user/test", "-c", "sleep 30"}, f.n, runtime.GOOS, "/bin/sh", TmuxFastPathIO{Input: strings.NewReader(""), Output: &out, Error: &stderr})
			if e != nil {
				t.Fatal(e)
			}
			if kind == "failure" || kind == "empty-matcher" || kind == "simple" {
				expected := "WorktreeCreate hook failed: no successful output"
				if kind == "failure" {
					expected = "actual native failure"
				}
				if result.Handled || !strings.Contains(result.Error, expected) {
					t.Fatal(result, out.String(), stderr.String())
				}
				if _, e = os.Stat(marker); !os.IsNotExist(e) {
					t.Fatal("failed/simple hook unexpectedly wrote", e)
				}
				return
			}
			if !result.Handled || result.Error != "" || !strings.Contains(out.String(), "Using worktree via hook: "+hookDir) {
				t.Fatal(result, out.String(), stderr.String())
			}
			raw, e := os.ReadFile(marker)
			if e != nil {
				t.Fatal(e)
			}
			var input map[string]any
			if e = json.Unmarshal(raw, &input); e != nil {
				t.Fatal(e, string(raw))
			}
			sid, _ := input["session_id"].(string)
			if sid == "" || input["cwd"] != resolvedTmuxFixturePath(t, f.cwd) || input["name"] != "user/test" || input["hook_event_name"] != "WorktreeCreate" || !strings.HasSuffix(fmt.Sprint(input["transcript_path"]), sid+".jsonl") {
				t.Fatal(input)
			}
			if (kind == "http" || kind == "http-sandbox") && calls != 1 {
				t.Fatal("HTTP producer count", calls)
			}
			if r := run("display-message", "-p", "-t", "project_worktree-user+test", "#{pane_current_path}"); r.Code != 0 || strings.TrimSpace(r.Stdout) != resolvedTmuxFixturePath(t, hookDir) {
				t.Fatal("actual tmux hook CWD", r)
			}
			entries, e := os.ReadDir(filepath.Join(f.config, "projects"))
			if e == nil && len(entries) > 0 {
				t.Fatal("preCLI wrote transcript", entries)
			}
		})
	}
}

func TestNativeTmuxFastPathRealGitCreateResumePrefixEnvironmentAndNoASK(t *testing.T) {
	f, _, run := privateTmuxFastFixture(t)
	f.n.Environment["GIT_AUTHOR_NAME"] = "fixture"
	f.n.Environment["GIT_AUTHOR_EMAIL"] = "fixture@example.invalid"
	f.n.Environment["GIT_COMMITTER_NAME"] = "fixture"
	f.n.Environment["GIT_COMMITTER_EMAIL"] = "fixture@example.invalid"
	f.write(t, filepath.Join(f.cwd, "tracked.txt"), "actual private source bytes")
	f.write(t, filepath.Join(f.cwd, ".gitignore"), "node_modules\n.claude/settings.local.json\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "settings.local.json"), `{"future":{"retained":true}}`)
	if e := os.MkdirAll(filepath.Join(f.cwd, "node_modules"), 0700); e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{"worktree":{"symlinkDirectories":["node_modules"]}}`)
	for _, a := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "private fixture"}} {
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: a, CWD: f.cwd, Environment: f.n.Environment})
		if e != nil || r.Code != 0 {
			t.Fatal(a, r, e)
		}
	}
	// Private local-only origin proves default-branch fetch; public network is
	// denied by the inherited test sandbox even if a future caller regresses.
	origin := filepath.Join(f.home, "origin.git")
	r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: []string{"clone", "--bare", f.cwd, origin}, CWD: f.cwd, Environment: f.n.Environment})
	if e != nil || r.Code != 0 {
		t.Fatal(r, e)
	}
	for _, a := range [][]string{{"remote", "add", "origin", origin}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}} {
		r, e = worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: a, CWD: f.cwd, Environment: f.n.Environment})
		if e != nil || r.Code != 0 {
			t.Fatal(r, e)
		}
	}

	tree := filepath.Join(resolvedTmuxFixturePath(t, f.cwd), ".claude", "worktrees", "user+actual")
	marker := filepath.Join(f.home, "tmux-child.json")
	script := filepath.Join(f.home, "child.py")
	f.write(t, script, "import os,json,pathlib,time\npathlib.Path("+fmt.Sprintf("%q", marker)+").write_text(json.dumps({'cwd':os.getcwd(),'session':os.environ.get('CLAUDE_CODE_TMUX_SESSION'),'prefix':os.environ.get('CLAUDE_CODE_TMUX_PREFIX'),'conflicts':os.environ.get('CLAUDE_CODE_TMUX_PREFIX_CONFLICTS'),'args':__import__('sys').argv[1:]}))\ntime.sleep(30)\n")
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	invoke := func() string {
		t.Helper()
		var out, stderr bytes.Buffer
		result, e := RunNativeTmuxWorktree(ctx, []string{"--tmux=classic", "-w", "user/actual", script, "raw argument", "--unknown"}, f.n, runtime.GOOS, python, TmuxFastPathIO{Input: strings.NewReader(""), Output: &out, Error: &stderr})
		if e != nil || !result.Handled || result.Error != "" {
			t.Fatal(result, e, out.String(), stderr.String())
		}
		return out.String()
	}
	if text := invoke(); !strings.Contains(text, "Created worktree: "+tree+" (based on origin/main)") {
		t.Fatal(text)
	}
	deadline := time.Now().Add(5 * time.Second)
	var child map[string]any
	for {
		raw, e := os.ReadFile(marker)
		if e == nil {
			e = json.Unmarshal(raw, &child)
			if e == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual tmux child never wrote", e)
		}
		time.Sleep(time.Millisecond * 10)
	}
	if child["cwd"] != tree || child["session"] != "project_worktree-user+actual" || child["prefix"] != "C-b" || child["conflicts"] != "1" {
		t.Fatal(child)
	}
	args, _ := json.Marshal(child["args"])
	if string(args) != `["raw argument","--unknown"]` {
		t.Fatal("inner raw argv changed", string(args))
	}
	if data, e := os.ReadFile(filepath.Join(tree, "tracked.txt")); e != nil || string(data) != "actual private source bytes" {
		t.Fatal(string(data), e)
	}
	if data, e := os.ReadFile(filepath.Join(tree, ".claude", "settings.local.json")); e != nil || string(data) != `{"future":{"retained":true}}` {
		t.Fatal(string(data), e)
	}
	if link, e := os.Readlink(filepath.Join(tree, "node_modules")); e != nil || link != filepath.Join(resolvedTmuxFixturePath(t, f.cwd), "node_modules") {
		t.Fatal(link, e)
	}
	// Resume must reuse the already-running pane, not replace its command or
	// repeat native create/post-setup. A user edit remains byte-identical.
	if r = run("set-option", "-g", "prefix", "C-a"); r.Code != 0 {
		t.Fatal(r)
	}
	f.write(t, filepath.Join(tree, "tracked.txt"), "retain dirty worktree")
	if text := invoke(); text != "" {
		t.Fatal("existing worktree recreated", text)
	}
	if data, e := os.ReadFile(filepath.Join(tree, "tracked.txt")); e != nil || string(data) != "retain dirty worktree" {
		t.Fatal(string(data), e)
	}
	if data, e := os.ReadFile(marker); e != nil || len(data) == 0 {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(f.config, "projects")); !os.IsNotExist(e) {
		t.Fatal("outer initialized a session transcript", e)
	}
}

func resolvedTmuxFixturePath(t *testing.T, path string) string {
	t.Helper()
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil {
		t.Fatal(e)
	}
	return resolved
}
