package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIParentPaneRosterPRVisibilityShutdownAndNextWritePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY and owned tmux; ConPTY separate")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("owned actual tmux binary required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-team-footer-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cli := filepath.Join(home, "claude-haha-go")
	build := exec.CommandContext(ctx, "go", "build", "-tags=native_process_fixture", "-p=4", "-o", cli, "../../cmd/claude-haha")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("owned offline current CLI build", err, string(output))
	}
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_team_footer_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TEAM_FOOTER_PTY_HELPER=1", "CC_HAHA_TEAM_FOOTER_CLI=" + cli}
	if directory := gochildcoverage.IndirectEnvironment(t, cli); directory != "" {
		cmd.Env = append(cmd.Env, "CC_HAHA_TEAM_FOOTER_CLI_COUNTERS="+directory)
	}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual pane-team footer PTY %v %.10000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) || !strings.Contains(string(output), `"actualChildWrite": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIParentPaneTeamFooterPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TEAM_FOOTER_PTY_HELPER") != "1" {
		t.Skip("owned real-daemon PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-team-footer-pty")); err != nil {
		t.Fatal(err)
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli := os.Getenv("CC_HAHA_TEAM_FOOTER_CLI")
	if cli != filepath.Join(home, "claude-haha-go") {
		t.Fatal("missing current owned tagged CLI binary")
	}
	private, err := os.MkdirTemp("/tmp", "footer-public-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(private)
	var mu sync.Mutex
	parentPosts, childPosts := 0, 0
	spawnSteps, shutdownSteps, writeSteps := 0, 0, 0
	var childArtifact, parentArtifact string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		value := obj(body)
		messages := string(value.Get("messages"))
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(string(value.Get("system")), "# Agent Teammate Communication") {
			childPosts++
			switch childPosts {
			case 1:
				response(w, "pane-footer-child-write", "Write", map[string]any{"file_path": childArtifact, "content": "PUBLIC_PANE_FOOTER_CHILD_WRITE_742"}, "")
			case 2:
				response(w, "pane-footer-child-idle", "", nil, "actual private pane worker idle")
			default:
				id := ""
				var rows []protocol.JSONObject
				_ = json.Unmarshal(value.Get("messages"), &rows)
				for _, row := range rows {
					text := row.String("content")
					if text == "" {
						for _, block := range array(row.Get("content")) {
							if block.String("type") == "text" {
								text += block.String("text")
							}
						}
					}
					start := strings.Index(text, `{"type":"shutdown_request"`)
					if start < 0 {
						continue
					}
					end := strings.Index(text[start:], "\n</teammate-message>")
					if end >= 0 {
						id = obj([]byte(text[start : start+end])).String("requestId")
					}
				}
				if id == "" {
					t.Error("actual child missed Source shutdown envelope")
					http.Error(w, "missing private shutdown", 500)
					return
				}
				response(w, "pane-footer-child-approved", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": id, "approve": true}}, "")
			}
			return
		}
		parentPosts++
		if strings.Contains(messages, "WRITE_PARENT_AFTER_PANE_FOOTER_742") {
			writeSteps++
			if writeSteps == 1 {
				response(w, "pane-footer-parent-write", "Write", map[string]any{"file_path": parentArtifact, "content": "PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742"}, "")
			} else {
				response(w, "pane-footer-parent-done", "", nil, "PANE_FOOTER_NEXT_WRITE_DONE_742")
			}
		} else if strings.Contains(messages, "SHUTDOWN_PRIVATE_PANE_FOOTER_742") {
			shutdownSteps++
			if shutdownSteps == 1 {
				response(w, "pane-footer-shutdown-request", "SendMessage", map[string]any{"to": "worker", "message": map[string]any{"type": "shutdown_request", "reason": "Actual public footer request"}}, "")
			} else {
				response(w, "pane-footer-shutdown-done", "", nil, "PANE_FOOTER_SHUTDOWN_REQUEST_DONE_742")
			}
		} else {
			spawnSteps++
			switch spawnSteps {
			case 1:
				response(w, "pane-footer-create", "TeamCreate", map[string]any{"team_name": "public-pane-footer"}, "")
			case 2:
				response(w, "pane-footer-spawn", "Agent", map[string]any{"name": "worker", "description": "Actual public pane worker", "prompt": "Write the actual private pane artifact and await shutdown.", "subagent_type": "general-purpose"}, "")
			default:
				response(w, "pane-footer-spawn-done", "", nil, "PANE_FOOTER_SPAWN_DONE_742")
			}
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Native.Temp = filepath.Join(o.Native.Home, "tmp")
	_ = os.MkdirAll(o.Native.Temp, 0700)
	o.Interactive = true
	o.Arguments.PermissionMode = "bypassPermissions"
	o.Arguments.TeammateMode = "tmux"
	o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
	o.Arguments.SystemPrompt = "Parent public pane footer harness"
	childArtifact = filepath.Join(o.WorkDir, "pane-child-write.txt")
	parentArtifact = filepath.Join(o.WorkDir, "pane-parent-write.txt")
	for _, args := range [][]string{{"init", "--initial-branch=private-pane-footer"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"}} {
		result, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(o.Environment), Arguments: args, CWD: o.WorkDir, Environment: o.Environment, IgnoreStdin: true})
		if err != nil || result.Code != 0 {
			t.Fatal(err, result.Stderr)
		}
	}
	bin := filepath.Join(home, "private-bin")
	_ = os.MkdirAll(bin, 0700)
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nprintf '%s' '{\"number\":742,\"url\":\"https://example.invalid/pr/742\",\"reviewDecision\":\"APPROVED\",\"isDraft\":false,\"headRefName\":\"private-pane-footer\",\"state\":\"OPEN\"}'\n"), 0700)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":" + filepath.Dir(tmux) + ":/usr/bin:/bin"
		env["CC_HAHA_NATIVE_FIXTURE_ROOT"] = filepath.Dir(o.Native.Home)
		env["TMPDIR"] = filepath.Join(filepath.Dir(o.Native.Home), "pane-tmp")
		env["TMUX_TMPDIR"] = private
		env["TERM"] = "xterm-256color"
		env["CLAUDE_CODE_TEAMMATE_COMMAND"] = cli
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		delete(env, "TMUX")
		delete(env, "ITERM_SESSION_ID")
		delete(env, "TERM_PROGRAM")
	}
	if err = os.MkdirAll(o.Environment["TMPDIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if directory := os.Getenv("CC_HAHA_TEAM_FOOTER_CLI_COUNTERS"); directory != "" {
		if testing.CoverMode() != "atomic" {
			t.Fatal("private pane counters require the actual instrumented helper")
		}
		o.Environment["GOCOVERDIR"] = directory
		o.Native.Environment["GOCOVERDIR"] = directory
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"alwaysThinkingEnabled":false,"permissions":{"defaultMode":"bypassPermissions"},"futurePane":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "futurePaneGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "pane-footer-paths.json"), raw(map[string]any{"child": childArtifact, "parent": parentArtifact, "team": filepath.Join(config, "teams", "public-pane-footer", "config.json"), "daemon": private, "label": "claude-swarm-" + fmt.Sprint(os.Getpid())}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	childData, childErr := os.ReadFile(childArtifact)
	parentData, parentErr := os.ReadFile(parentArtifact)
	global, _ := os.ReadFile(filepath.Join(config, ".claude.json"))
	user, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	mu.Lock()
	parentCount, childCount := parentPosts, childPosts
	mu.Unlock()
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "actualChildWrite": childErr == nil && string(childData) == "PUBLIC_PANE_FOOTER_CHILD_WRITE_742", "sameHarnessParentWrite": parentErr == nil && string(parentData) == "PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742", "unknownFieldsPreserved": string(obj(obj(global).Get("futurePaneGlobal")).Get("keep")) == "true" && string(obj(obj(user).Get("futurePane")).Get("keep")) == "true", "parentPosts": parentCount, "childPosts": childCount, "exitCode": code}
	paneBytes, paneErr := os.ReadFile(filepath.Join(home, "pane-footer-actual-pane.txt"))
	pane := strings.TrimSpace(string(paneBytes))
	list := exec.CommandContext(ctx, tmux, "-L", "claude-swarm-"+fmt.Sprint(os.Getpid()), "list-panes", "-a", "-F", "#{pane_id}")
	list.Env = []string{"HOME=" + o.Native.Home, "PATH=" + filepath.Dir(tmux) + ":/usr/bin:/bin", "TMUX_TMPDIR=" + private}
	capturePublicGoCoverage(t, list)
	output, listErr := list.CombinedOutput()
	joined := paneErr == nil && pane != ""
	if listErr != nil {
		joined = joined && (strings.Contains(string(output), "no server") || strings.Contains(strings.ToLower(string(output)), "no such file"))
	} else {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			joined = joined && line != pane
		}
	}
	receipt["privatePanesJoined"] = joined
	_ = os.WriteFile(filepath.Join(home, "pane-footer-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "pane-footer-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !receipt["terminalRestored"].(bool) || !receipt["actualChildWrite"].(bool) || !receipt["sameHarnessParentWrite"].(bool) || !receipt["unknownFieldsPreserved"].(bool) || !receipt["privatePanesJoined"].(bool) || childCount != 3 || parentCount < 7 {
		t.Fatal(receipt)
	}
}
