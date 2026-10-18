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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIRemoteTasksInitialMountResumeStopNextHTTPAndGlobalExitPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"initial-history", "slash-resume"} {
		t.Run(profile, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "owned-remote-task-pty"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "testdata/terminal_remote_tasks_pty.py", binary, root, profile)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_REMOTE_TASK_PTY_HELPER=1", "CC_HAHA_REMOTE_TASK_PTY_PROFILE=" + profile}
			capturePublicGoCoverage(t, cmd)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("actual public RunCommand remote task PTY", err, string(out))
			}
			for _, proof := range []string{`"actualPublicRunCommand": true`, `"models": 2`, `"archives": 1`, `"globalExitPreservesRemote": true`, `"terminalRestored": true`, `"exitCode": 0`} {
				if !strings.Contains(string(out), proof) {
					t.Fatal(proof, string(out))
				}
			}
			t.Log("actual public RunCommand remote task receipt", string(out))
		})
	}
}

func TestNativeTerminalRemoteTasksPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_REMOTE_TASK_PTY_HELPER") != "1" {
		t.Skip("owned public PTY helper")
	}
	root := os.Getenv("HOME")
	if marker, err := os.ReadFile(filepath.Join(root, "owned-remote-task-pty")); err != nil || string(marker) != "private" {
		t.Fatal("explicit private PTY guard", err)
	}
	profile := os.Getenv("CC_HAHA_REMOTE_TASK_PTY_PROFILE")
	if profile != "initial-history" && profile != "slash-resume" {
		t.Fatal(profile)
	}
	var models, archives atomic.Int32
	var mu sync.Mutex
	var bodies []string
	heldJoined, exitJoined := make(chan struct{}), make(chan struct{})
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-usage-token" || r.Header.Get("x-organization-uuid") != "owned-remote-task-org" {
			t.Error("real task HTTP headers", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions/session-owned-held", "/v1/sessions/session-owned-exit":
			_, _ = w.Write([]byte(`{"session_status":"running","session_context":{}}`))
		case "/v1/sessions/session-owned-held/events", "/v1/sessions/session-owned-exit/events":
			label := "held"
			joined := heldJoined
			if strings.Contains(r.URL.Path, "owned-exit/") {
				label = "exit"
				joined = exitJoined
			}
			if err := os.WriteFile(filepath.Join(root, "remote-"+label+"-started"), []byte(r.URL.Path), 0600); err != nil {
				t.Error(err)
			}
			defer close(joined)
			for {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		case "/v1/sessions/session-owned-held/archive":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != "{}" {
				t.Error("actual archive wire", r.Method, string(body))
			}
			archives.Add(1)
			if err := os.WriteFile(filepath.Join(root, "remote-held-archived"), body, 0600); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Error("unowned PTY task API", r.URL)
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		if !strings.Contains(string(body), "OLD_REMOTE_TASK_RESUME_CONTEXT") || strings.Contains(string(body), "<status>completed</status>") {
			t.Error("same resumed history/notification", string(body))
		}
		n := models.Add(1)
		response(w, "public-remote-task", "", nil, "PUBLIC_REMOTE_TASK_NEXT_HUMAN_COMPLETE_"+strconv.Itoa(int(n)))
	}))
	defer model.Close()
	o := terminalRemoteTaskOptions(t, model.URL, proxy, ca)
	o.Stderr = os.Stderr
	held := seedTerminalRemoteTask(t, o, "held")
	exit := seedTerminalRemoteTask(t, o, "exit")
	exitOriginal, err := os.ReadFile(exit)
	if err != nil {
		t.Fatal(err)
	}
	if profile == "initial-history" {
		o.Arguments.SessionID = ""
		o.Arguments.Resume = remoteTaskResumeSID
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var environment []string
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	environment = append(environment, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal("ordinary public CLI close", code, err)
	}
	beforeProbe, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	pendingInput, err := nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	for _, joined := range []<-chan struct{}{heldJoined, exitJoined} {
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("actual task HTTP close not joined")
		}
	}
	_, heldErr := os.Stat(held)
	exitAfter, exitErr := os.ReadFile(exit)
	mu.Lock()
	requestBodies := append([]string(nil), bodies...)
	mu.Unlock()
	if len(requestBodies) != 2 || !strings.Contains(requestBodies[0], "PUBLIC_HUMAN_WHILE_REMOTE_HELD") || !strings.Contains(requestBodies[1], "PUBLIC_HUMAN_AFTER_REMOTE_TASK_STOP") {
		t.Fatal("actual model human order", requestBodies)
	}
	receipt := map[string]any{"actualPublicRunCommand": true, "models": models.Load(), "archives": archives.Load(), "globalExitPreservesRemote": os.IsNotExist(heldErr) && exitErr == nil && string(exitAfter) == string(exitOriginal), "terminalRestored": reflect.DeepEqual(before, after), "exitCode": code, "termiosBefore": fmt.Sprintf("%#v", before), "termiosBeforeNativeProbe": fmt.Sprintf("%#v", beforeProbe), "pendingInputNativeProbe": pendingInput, "termiosAfter": fmt.Sprintf("%#v", after)}
	if err = os.WriteFile(filepath.Join(root, "remote-task-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "remote-task-models.json"), raw(requestBodies), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(root, "remote-task-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parent did not observe physical termios")
		}
		time.Sleep(10 * time.Millisecond)
	}
	encoded, _ := json.Marshal(receipt)
	t.Log(string(encoded))
}
