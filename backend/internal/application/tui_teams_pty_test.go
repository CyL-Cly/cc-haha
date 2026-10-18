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

func TestActualCLITeamsFooterPaneDialogActionsAndNextWritePTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-teams-dialog-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cli := filepath.Join(home, "claude-haha-go")
	build := exec.CommandContext(ctx, "go", "build", "-tags=native_process_fixture", "-p=2", "-o", cli, "../../cmd/claude-haha")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("owned offline current CLI build", err, string(output))
	}
	cmd := exec.CommandContext(ctx, python, "testdata/tui_teams_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TEAMS_DIALOG_PTY_HELPER=1", "CC_HAHA_TEAM_FOOTER_CLI=" + cli}
	if directory := gochildcoverage.IndirectEnvironment(t, cli); directory != "" {
		cmd.Env = append(cmd.Env, "CC_HAHA_TEAM_FOOTER_CLI_COUNTERS="+directory)
	}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		capture, _ := os.ReadFile(filepath.Join(home, "teams-dialog-running-terminal.txt"))
		receipt, _ := os.ReadFile(filepath.Join(home, "teams-dialog-receipt.json"))
		bodies, _ := os.ReadFile(filepath.Join(home, "teams-dialog-model-bodies.json"))
		if len(bodies) > 0 {
			_ = os.WriteFile(filepath.Join("../../../artifacts/go-migration-20261013", "tui-teams-public-current-observed-bodies.json"), bodies, 0600)
		}
		t.Fatalf("actual Teams dialog PTY %v %.10000s\nRECEIPT %s\nTERMINAL %.22000s", err, output, receipt, capture)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) || !strings.Contains(string(output), `"actualChildWrite": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLITeamsDialogPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TEAMS_DIALOG_PTY_HELPER") != "1" {
		t.Skip("owned real-daemon PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-teams-dialog-pty")); err != nil {
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
	private, err := os.MkdirTemp("/tmp", "teams-public-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(private)
	var mu sync.Mutex
	parentPosts, childPosts, parentInboxPosts := 0, 0, 0
	inboxNotifications := map[string]int{}
	spawnSteps, writeSteps := 0, 0
	childSteps := map[string]int{}
	var childArtifact, parentArtifact string
	var observedBodies []json.RawMessage
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
		observedBodies = append(observedBodies, append(json.RawMessage(nil), body...))
		if strings.Contains(string(value.Get("system")), "# Agent Teammate Communication") {
			childPosts++
			name := ""
			for _, candidate := range []string{"one", "two", "three"} {
				if strings.Contains(messages, "TEAMS_WORKER_"+strings.ToUpper(candidate)+"_928") {
					name = candidate
					break
				}
			}
			if name == "" {
				t.Error("actual child identity marker missing")
				http.Error(w, "missing identity", 500)
				return
			}
			childSteps[name]++
			switch childSteps[name] {
			case 1:
				response(w, "teams-child-write-"+name, "Write", map[string]any{"file_path": childArtifact + "-" + name, "content": "PUBLIC_TEAMS_CHILD_WRITE_" + name}, "")
			case 2:
				response(w, "teams-child-idle-"+name, "", nil, "actual private teams pane worker idle")
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
				if name != "one" || id == "" {
					t.Error("unexpected real Teams pane model followup", name)
					http.Error(w, "missing private shutdown", 500)
					return
				}
				response(w, "teams-child-approved", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": id, "approve": true}}, "")
			}
			return
		}
		parentPosts++
		notifications, incoming, notificationErr := terminalTeamsModelNotifications(value)
		if notificationErr != nil {
			t.Error("unexpected actual parent model query", notificationErr)
		}
		if incoming {
			parentInboxPosts++
			for _, key := range notifications {
				inboxNotifications[key]++
				if inboxNotifications[key] != 1 {
					t.Error("single physical notification producer appeared more than once", key)
				}
			}
			// The real roster/pane cleanup precedes the independent inbox query.
			// Publish only what this loopback provider has actually received.
			if err := os.WriteFile(filepath.Join(home, "teams-dialog-inbox-model-receipt.json"), raw(inboxNotifications), 0600); err != nil {
				t.Error("actual inbox model observation", err)
			}
			response(w, "teams-actual-inbox", "", nil, "PUBLIC_TEAMS_INBOX_PROCESSED_928")
			return
		}
		if strings.Contains(messages, "WRITE_PARENT_AFTER_PANE_FOOTER_742") {
			writeSteps++
			if writeSteps == 1 {
				response(w, "teams-dialog-parent-write", "Write", map[string]any{"file_path": parentArtifact, "content": "PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742"}, "")
			} else {
				response(w, "teams-dialog-parent-done", "", nil, "PANE_FOOTER_NEXT_WRITE_DONE_742")
			}

		} else {
			spawnSteps++
			switch spawnSteps {
			case 1:
				response(w, "teams-create", "TeamCreate", map[string]any{"team_name": "public-teams-dialog"}, "")
			case 2, 3, 4:
				name := []string{"one", "two", "three"}[spawnSteps-2]
				response(w, "teams-spawn-"+name, "Agent", map[string]any{"name": name, "description": "Actual public Teams pane " + name, "prompt": "TEAMS_WORKER_" + strings.ToUpper(name) + "_928: Write the actual private pane artifact and await shutdown.", "subagent_type": "general-purpose"}, "")
			default:
				response(w, "teams-spawn-done", "", nil, "TEAMS_DIALOG_SPAWN_DONE_928")
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
	childArtifact = filepath.Join(o.WorkDir, "teams-child-write.txt")
	parentArtifact = filepath.Join(o.WorkDir, "teams-parent-write.txt")
	for _, args := range [][]string{{"init", "--initial-branch=private-teams-dialog"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"}} {
		result, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(o.Environment), Arguments: args, CWD: o.WorkDir, Environment: o.Environment, IgnoreStdin: true})
		if err != nil || result.Code != 0 {
			t.Fatal(err, result.Stderr)
		}
	}
	bin := filepath.Join(home, "private-bin")
	_ = os.MkdirAll(bin, 0700)
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nprintf '%s' '{\"number\":742,\"url\":\"https://example.invalid/pr/742\",\"reviewDecision\":\"APPROVED\",\"isDraft\":false,\"headRefName\":\"private-teams-dialog\",\"state\":\"OPEN\"}'\n"), 0700)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":" + filepath.Dir(tmux) + ":/usr/bin:/bin"
		env["CC_HAHA_NATIVE_FIXTURE_ROOT"] = filepath.Dir(o.Native.Home)
		env["TMPDIR"] = filepath.Join(filepath.Dir(o.Native.Home), "pane-tmp")
		env["TMUX_TMPDIR"] = private
		env["TERM"] = "xterm-256color"
		env["NO_PROXY"] = "127.0.0.1,localhost"
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
	_ = os.WriteFile(filepath.Join(home, "teams-dialog-paths.json"), raw(map[string]any{"child": childArtifact, "parent": parentArtifact, "team": filepath.Join(config, "teams", "public-teams-dialog", "config.json"), "daemon": private, "label": "claude-swarm-" + fmt.Sprint(os.Getpid())}), 0600)
	seedTerminalReleaseWarmFixture(t, o)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, terminalTeamsFixtureEnvironment(o.Environment))
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
	childData, childErr := os.ReadFile(childArtifact + "-one")
	parentData, parentErr := os.ReadFile(parentArtifact)
	global, _ := os.ReadFile(filepath.Join(config, ".claude.json"))
	user, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	mu.Lock()
	parentCount, childCount, parentIncomingCount := parentPosts, childPosts, parentInboxPosts
	notificationsComplete := len(inboxNotifications) == 7
	for _, name := range []string{"idle:one", "idle:two", "idle:three", "terminated:one", "terminated:two", "terminated:three", "approved:one"} {
		notificationsComplete = notificationsComplete && inboxNotifications[name] == 1
	}
	_ = os.WriteFile(filepath.Join(home, "teams-dialog-model-bodies.json"), raw(observedBodies), 0600)
	_ = os.WriteFile(filepath.Join(home, "teams-dialog-inbox-model-receipt.json"), raw(inboxNotifications), 0600)
	mu.Unlock()
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "actualChildWrite": childErr == nil && string(childData) == "PUBLIC_TEAMS_CHILD_WRITE_one", "sameHarnessParentWrite": parentErr == nil && string(parentData) == "PUBLIC_PANE_FOOTER_NEXT_PARENT_WRITE_742", "unknownFieldsPreserved": string(obj(obj(global).Get("futurePaneGlobal")).Get("keep")) == "true" && string(obj(obj(user).Get("futurePane")).Get("keep")) == "true", "parentPosts": parentCount, "childPosts": childCount, "exitCode": code}
	receipt["parentCorePosts"], receipt["validatedParentInboxPosts"], receipt["exactActualInboxNotifications"] = parentCount-parentIncomingCount, parentIncomingCount, notificationsComplete
	paneBytes, paneErr := os.ReadFile(filepath.Join(home, "teams-dialog-actual-pane.txt"))
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
	_ = os.WriteFile(filepath.Join(home, "teams-dialog-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "teams-dialog-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Original useInboxPoller and handleIncomingPrompt submit genuine idle,
	// trusted shutdown approval and generated termination envelopes, even while
	// Teams JSX remains mounted. Physical polling may split those seven events
	// into several batches; retain the exact seven core parent and child POSTs.
	if !receipt["terminalRestored"].(bool) || !receipt["actualChildWrite"].(bool) || !receipt["sameHarnessParentWrite"].(bool) || !receipt["unknownFieldsPreserved"].(bool) || !receipt["privatePanesJoined"].(bool) || childCount != 7 || parentCount-parentIncomingCount != 7 || spawnSteps != 5 || writeSteps != 2 || !notificationsComplete || parentIncomingCount < 1 || parentIncomingCount > 7 {
		t.Fatal(receipt)
	}
}

func terminalTeamsFixtureEnvironment(value map[string]string) []string {
	env := make([]string, 0, len(value))
	for key, item := range value {
		env = append(env, key+"="+item)
	}
	return env
}
