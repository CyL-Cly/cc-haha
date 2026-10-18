//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/charmbracelet/x/term"
)

func TestActualRunCommandExternalTeammateXMLNextHumanAndRestoredPTY(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-teammate-origin-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_teammate_message_pty.py", binary, home)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_TEAMMATE_ORIGIN_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(`"terminalRestored": true`)) {
		screen, _ := os.ReadFile(filepath.Join(home, "teammate-origin-screen.txt"))
		stage, _ := os.ReadFile(filepath.Join(home, "teammate-origin-stage.txt"))
		t.Fatalf("actual teammate attachment origin %v %s stage%s screen%s", err, output, stage, screen)
	}
}

func TestNativeTerminalTeammateMessagePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TEAMMATE_ORIGIN_PTY_HELPER") != "1" {
		t.Skip("private teammate origin PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-teammate-origin-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var target, config string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("private model transport", err, r.Header)
		}
		n := requests.Add(1)
		if err = os.WriteFile(filepath.Join(home, "teammate-origin-request-"+string(rune('0'+n))+".json"), body, 0600); err != nil {
			t.Error(err)
		}
		switch n {
		case 1:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_FIRST_HUMAN")) {
				t.Error("first human missing", string(body))
			}
			response(w, "origin-team", "TeamCreate", map[string]any{"team_name": "actual-origin-team"}, "")
		case 2:
			store, e := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
			if e != nil {
				t.Error(e)
				http.Error(w, "private task store", 500)
				return
			}
			mailbox, e := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: store})
			if e != nil {
				t.Error(e)
				http.Error(w, "private mailbox", 500)
				return
			}
			text := string(raw(map[string]any{"type": "task_assignment", "taskId": "12", "subject": "PUBLIC_ORIGIN_ASSIGNED_TASK", "description": "actual durable mailbox attachment", "assignedBy": "team-lead", "timestamp": "2026-10-01T00:00:00.000Z"}))
			e = mailbox.Write("team-lead", "actual-origin-team", protocol.NewJSONObject(field("from", "worker"), field("text", text), field("timestamp", "actual-origin-message"), field("summary", "actual assignment summary"), field("color", "blue")))
			if e != nil {
				t.Error(e)
				http.Error(w, "private mailbox write", 500)
				return
			}
			response(w, "origin-read", "Read", map[string]any{"file_path": target}, "")
		case 3:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_PHYSICAL_DATA")) || bytes.Contains(body, []byte("PUBLIC_ORIGIN_ASSIGNED_TASK")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("actual Read/mailbox attachment missing", string(body))
			}
			response(w, "origin-attachment-done", "", nil, "PUBLIC_ORIGIN_ATTACHMENT_DONE")
		case 4:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_ASSIGNED_TASK")) || !bytes.Contains(body, []byte("<teammate-message teammate_id=\\\"worker\\\"")) || bytes.Contains(body, []byte("PUBLIC_ORIGIN_NEXT_HUMAN")) {
				t.Error("actual external idle XML input missing", string(body))
			}
			response(w, "origin-mailbox-read", "Read", map[string]any{"file_path": target}, "")
		case 5:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_PHYSICAL_DATA")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("autonomous same Harness Read missing", string(body))
			}
			response(w, "origin-mailbox-done", "", nil, "PUBLIC_ORIGIN_MAILBOX_DONE")
		case 6:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_NEXT_HUMAN")) || !bytes.Contains(body, []byte("PUBLIC_ORIGIN_FIRST_HUMAN")) || !bytes.Contains(body, []byte("PUBLIC_ORIGIN_ASSIGNED_TASK")) || bytes.Contains(body, []byte("TeammateOrigin")) {
				t.Error("same Harness normalized canonical history", string(body))
			}
			response(w, "origin-next-read", "Read", map[string]any{"file_path": target}, "")
		case 7:
			if !bytes.Contains(body, []byte("PUBLIC_ORIGIN_PHYSICAL_DATA")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("next actual Read missing", string(body))
			}
			response(w, "origin-next-done", "", nil, "PUBLIC_ORIGIN_NEXT_DONE")
		default:
			t.Error("unexpected model request", n, string(body))
			http.Error(w, "extra model request", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}
	config = o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"hasCompletedOnboarding":true,"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"agentTeamsEnabled":true,"prefersReducedMotion":true,"spinnerTipsEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(o.WorkDir, "actual-origin-physical.txt")
	if err := os.WriteFile(target, []byte("PUBLIC_ORIGIN_PHYSICAL_DATA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, result, err := ParseCommand([]string{"--model", "fixture-model", "--session-id", o.Arguments.SessionID, "--tools", "TeamCreate,Read", "--permission-mode", "bypassPermissions"}, o.Version, o.WorkDir, home, o.Environment)
	if err != nil || result.Parsed == nil {
		t.Fatal("actual ParseCommand", err, result)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := []string{}
	for k, v := range o.Environment {
		env = append(env, k+"="+v)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	code, err := RunCommand(ctx, parsed, o, os.Stdin, os.Stdout, env)
	if _, e := nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); e != nil {
		t.Fatal(e)
	}
	after, stateErr := term.GetState(os.Stdin.Fd())
	if err != nil || code != 0 || stateErr != nil || !reflect.DeepEqual(before, after) || requests.Load() != 7 {
		t.Fatalf("actual origin close code%d err%v terminal%v HTTP%d", code, err, stateErr, requests.Load())
	}
	receipt, _ := json.Marshal(map[string]any{"code": code, "requests": requests.Load(), "terminalRestored": true})
	if err = os.WriteFile(filepath.Join(home, "teammate-origin-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if _, err = os.Stat(filepath.Join(home, "teammate-origin-restoration-inspected")); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("driver did not inspect live termios")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestActualRunCommandTeammateInternalOriginRequiresInternalHostProfile(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		posts.Add(1)
		http.Error(w, "internal origin must never reach model", 500)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["USER_TYPE"] = "ant"
	}
	parsed, result, err := ParseCommand([]string{"--model", "fixture-model"}, o.Version, o.WorkDir, o.Environment["HOME"], o.Environment)
	if err != nil || result.Parsed == nil {
		t.Fatal(err, result)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	environment := []string{}
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	code, err := RunCommand(ctx, parsed, o, strings.NewReader(""), io.Discard, environment)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "internal schema host profile") || posts.Load() != 0 {
		t.Fatal("External profile opened an internal attachment origin", code, err, posts.Load())
	}
}
