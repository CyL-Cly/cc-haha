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
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func worktreeMainFixture(t *testing.T) nativePluginFixture {
	t.Helper()
	f := newNativePluginFixture(t)
	f.cwd = f.n.CWD
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"worktree":{"symlinkDirectories":["node_modules"],"sparsePaths":["src"]},"future":{"keep":true}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-loopback-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["DISABLE_TELEMETRY"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["GIT_CONFIG_NOSYSTEM"] = "1"
	f.n.Environment["GIT_CONFIG_GLOBAL"] = filepath.Join(f.home, "absent-gitconfig")
	f.n.Environment["GIT_AUTHOR_NAME"] = "fixture"
	f.n.Environment["GIT_AUTHOR_EMAIL"] = "fixture@example.invalid"
	f.n.Environment["GIT_COMMITTER_NAME"] = "fixture"
	f.n.Environment["GIT_COMMITTER_EMAIL"] = "fixture@example.invalid"
	f.write(t, filepath.Join(f.cwd, "src", "tracked.txt"), "tracked fixture")
	f.write(t, filepath.Join(f.cwd, ".gitignore"), "node_modules\nsecret-dir/\n.claude/settings.local.json\n")
	f.write(t, filepath.Join(f.cwd, ".worktreeinclude"), "secret-dir/*\n!secret-dir/excluded.txt\n")
	f.write(t, filepath.Join(f.cwd, "secret-dir", "included.txt"), "private include")
	f.write(t, filepath.Join(f.cwd, "secret-dir", "excluded.txt"), "excluded")
	if e := os.MkdirAll(filepath.Join(f.cwd, "node_modules"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "private initial"}} {
		result, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: f.cwd, Environment: f.n.Environment, IgnoreStdin: true})
		if e != nil || result.Code != 0 {
			t.Fatal(args, result, e)
		}
	}
	return f
}

func emitWorktreeModel(w http.ResponseWriter, round int, name, input, text string) {
	recorder := httptest.NewRecorder()
	emitPlanFixture(recorder, round, name, text)
	data := recorder.Body.Bytes()
	if input != "" {
		quoted, _ := json.Marshal(input)
		data = bytes.Replace(data, []byte(`"partial_json":"{}"`), append(append([]byte(`"partial_json":`), quoted...), []byte{}...), 1)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write(data)
}

func TestActualSharedMainWorktreeEnterWriteReadRefuseDeleteKeep(t *testing.T) {
	f := worktreeMainFixture(t)
	var mu sync.Mutex
	requests := 0
	bodies := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		round := requests
		bodies = append(bodies, data)
		mu.Unlock()
		switch round {
		case 1:
			emitWorktreeModel(w, round, "EnterWorktree", `{"name":"private/feature"}`, "")
		case 2:
			emitWorktreeModel(w, round, "Write", `{"file_path":"proof.txt","content":"WORKTREE_PRIVATE_OK"}`, "")
		case 3:
			emitWorktreeModel(w, round, "Read", `{"file_path":"proof.txt"}`, "")
		case 4:
			emitWorktreeModel(w, round, "ExitWorktree", `{"action":"remove"}`, "")
		case 5:
			emitWorktreeModel(w, round, "ExitWorktree", `{"action":"keep"}`, "")
		case 6:
			emitWorktreeModel(w, round, "", "", "actual worktree flow complete")
		default:
			t.Errorf("unexpected model request %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	var errors bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual worktree fixture", Tools: []string{"EnterWorktree,ExitWorktree,Write,Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &errors})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	var input protocol.JSONObject
	_ = input.UnmarshalJSON([]byte(`{"type":"user","uuid":"worktree-user","message":{"role":"user","content":"create and verify a worktree"}}`))
	if e = m.RunTurn(context.Background(), input); e != nil {
		t.Fatalf("actual shared turn: %v; %s", e, errors.String())
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 6 {
		t.Fatalf("requests%d errors%s last%s", requests, errors.String(), bodies[len(bodies)-1])
	}
	path := worktree.PathFor(f.cwd, "private/feature")
	content, e := os.ReadFile(filepath.Join(path, "proof.txt"))
	if e != nil || string(content) != "WORKTREE_PRIVATE_OK" {
		t.Fatal("actual worktree relative Write", string(content), e)
	}
	if _, e = os.Stat(filepath.Join(f.cwd, "proof.txt")); !os.IsNotExist(e) {
		t.Fatal("relative Write leaked to original cwd", e)
	}
	if !bytes.Contains(bodies[3], []byte("WORKTREE_PRIVATE_OK")) || !bytes.Contains(bodies[4], []byte("Removing will discard this work permanently")) || !bytes.Contains(bodies[5], []byte("Your work is preserved at")) {
		t.Fatalf("actual result/validation feedback absent: %s", bodies[5])
	}
	if value, e := os.ReadFile(filepath.Join(path, "secret-dir", "included.txt")); e != nil || string(value) != "private include" {
		t.Fatal("actual include matcher", string(value), e)
	}
	if _, e = os.Stat(filepath.Join(path, "secret-dir", "excluded.txt")); !os.IsNotExist(e) {
		t.Fatal("include negation ignored", e)
	}
	if target, e := os.Readlink(filepath.Join(path, "node_modules")); e != nil || target != filepath.Join(f.cwd, "node_modules") {
		t.Fatal("configured symlink", target, e)
	}
	snapshot := m.state.Snapshot()
	if snapshot.CWD != f.cwd || snapshot.OriginalCWD != f.cwd || snapshot.ProjectRoot != f.cwd || m.worktreeState.current != nil {
		t.Fatal("actual owner cwd identity not restored", snapshot)
	}
	files, e := filepath.Glob(filepath.Join(f.config, "projects", "*", snapshot.SessionID+".jsonl"))
	if e != nil || len(files) != 1 {
		t.Fatal("cached transcript was retargeted", files, e)
	}
	data, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	var states []protocol.JSONObject
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var entry protocol.JSONObject
		if entry.UnmarshalJSON(line) == nil && entry.String("type") == "worktree-state" {
			states = append(states, entry)
		}
	}
	if len(states) != 3 || string(states[1].Get("worktreeSession")) != "null" || string(states[2].Get("worktreeSession")) != "null" {
		t.Fatalf("eager metadata entries %+v\n%s", states, data)
	}
	if bytes.Contains(states[0].Get("worktreeSession"), []byte("creationDurationMs")) || bytes.Contains(states[0].Get("worktreeSession"), []byte("usedSparsePaths")) {
		t.Fatal("ephemeral fields persisted")
	}
	global, e := m.Global.GetProject(f.cwd)
	if e != nil || global.Get("activeWorktreeSession") != nil {
		t.Fatal("active global state not cleared", global, e)
	}
}

func worktreeOnlyMain(t *testing.T, f nativePluginFixture) *MainPrint {
	t.Helper()
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"EnterWorktree,ExitWorktree"}, MaxTurns: 8, SessionID: "11111111-1111-4111-8111-111111111111", PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestWorktreeActualOwnerCanonicalLiftAndPlanSlug(t *testing.T) {
	f := worktreeMainFixture(t)
	// This exercises the original canonical-root lift before a git failure.
	f.n.CWD = filepath.Join(f.cwd, "src")
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"EnterWorktree,ExitWorktree"}, MaxTurns: 8, SessionID: "11111111-1111-4111-8111-111111111111", PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bindings, e := m.nativeWorktreeBindings()
		if e != nil {
			return e
		}
		before, e := m.plans.Slug(scope)
		if e != nil {
			return e
		}
		request, e := bindings.PrepareEnter(scope, protocol.NewJSONObject())
		if e != nil {
			return e
		}
		if request.CWD != f.cwd || request.Slug != before || m.state.Snapshot().CWD != f.cwd {
			return fmt.Errorf("canonical lift/slug %+v", request)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
