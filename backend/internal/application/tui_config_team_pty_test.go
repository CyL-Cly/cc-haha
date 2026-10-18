package application

import (
	"context"
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

	"github.com/charmbracelet/x/term"
)

func TestActualCLIConfigTeamsModeEscapeDefaultModelAndNextSharedHarnessPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-config-team-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_config_team_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONFIG_TEAM_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Config teams PTY %v %.8500s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIConfigTeamPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONFIG_TEAM_PTY_HELPER") != "1" {
		t.Skip("owned Config teams PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-config-team-pty")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	parentPosts, childPosts, spawnSteps, writeSteps := 0, 0, 0, 0
	var childFile, parentFile string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		input := obj(body)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(string(input.Get("system")), "# Agent Teammate Communication") {
			childPosts++
			if input.String("model") != "private-config-team-haiku" {
				t.Error("saved plain teammate default not consumed", input.String("model"))
			}
			if childPosts == 1 {
				response(w, "config-team-child-write", "Write", map[string]any{"file_path": childFile, "content": "PUBLIC_CONFIG_TEAM_CHILD_WRITE_684"}, "")
			} else {
				response(w, "config-team-child-done", "", nil, "CONFIG_TEAM_CHILD_DONE_684")
			}
			return
		}
		parentPosts++
		if input.String("model") != "private-config-team-sonnet" {
			t.Error("teammate picker changed actual leader", input.String("model"))
		}
		if strings.Contains(string(input.Get("messages")), "CONFIG_TEAM_NEXT_PARENT_WRITE_684") {
			writeSteps++
			if writeSteps == 1 {
				response(w, "config-team-parent-write", "Write", map[string]any{"file_path": parentFile, "content": "PUBLIC_CONFIG_TEAM_PARENT_WRITE_684"}, "")
			} else {
				response(w, "config-team-parent-done", "", nil, "CONFIG_TEAM_PARENT_DONE_684")
			}
			return
		}
		spawnSteps++
		switch spawnSteps {
		case 1:
			response(w, "config-team-create", "TeamCreate", map[string]any{"team_name": "public-config-team"}, "")
		case 2:
			response(w, "config-team-spawn", "Agent", map[string]any{"description": "Actual Config plain teammate", "name": "worker", "prompt": "Write the actual Config child artifact."}, "")
		default:
			response(w, "config-team-spawn-done", "", nil, "CONFIG_TEAM_SPAWN_DONE_684")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Model = "sonnet"
	o.Arguments.TeammateMode = "tmux"
	o.Arguments.PermissionMode = "bypassPermissions"
	o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
	o.Arguments.SystemPrompt = "Parent Config team Harness"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "private-config-team-sonnet"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "private-config-team-opus"
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "private-config-team-haiku"
		delete(env, "TMUX")
		delete(env, "TERM_PROGRAM")
		delete(env, "ITERM_SESSION_ID")
	}
	childFile = filepath.Join(o.WorkDir, "config-team-child.txt")
	parentFile = filepath.Join(o.WorkDir, "config-team-parent.txt")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	settings := filepath.Join(config, "settings.json")
	global := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(settings, []byte(`{"effortLevel":"medium","alwaysThinkingEnabled":false,"futureTeamUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(global, raw(map[string]any{"hasCompletedOnboarding": true, "futureTeamGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "config-team-paths.json"), raw(map[string]any{"settings": settings, "global": global, "child": childFile, "parent": parentFile}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
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
	childData, childErr := os.ReadFile(childFile)
	parentData, parentErr := os.ReadFile(parentFile)
	user, _ := os.ReadFile(settings)
	saved, _ := os.ReadFile(global)
	mu.Lock()
	parentCount, childCount := parentPosts, childPosts
	mu.Unlock()
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "parentPosts": parentCount, "childPosts": childCount, "actualChildWrite": childErr == nil && string(childData) == "PUBLIC_CONFIG_TEAM_CHILD_WRITE_684", "actualParentWrite": parentErr == nil && string(parentData) == "PUBLIC_CONFIG_TEAM_PARENT_WRITE_684", "unknownFieldsPreserved": string(obj(obj(saved).Get("futureTeamGlobal")).Get("keep")) == "true" && string(obj(obj(user).Get("futureTeamUser")).Get("keep")) == "true", "exitCode": code}
	_ = os.WriteFile(filepath.Join(home, "config-team-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "config-team-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if parentCount != 5 || childCount != 2 || !reflect.DeepEqual(before, after) || childErr != nil || parentErr != nil {
		t.Fatal(receipt)
	}
}
