package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestActualTerminalBashRunsLocallyRecordsOriginalHistoryAndContinuesSameHarness(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<bash-input>") || !strings.Contains(string(body), "LOCAL_BASH_REAL_A") || !strings.Contains(string(body), "local-command-caveat") {
			t.Error("actual local shell history missing in next model request")
		}
		response(w, "after-local-bash", "", nil, "AFTER_LOCAL_BASH_SHARED_HARNESS_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.ForegroundShellProgress = true
	session, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(context.Background())
	path := filepath.Join(o.WorkDir, "local-shell-artifact")
	events := []tui.Event{}
	emit := func(event tui.Event) error { events = append(events, event); return nil }
	for i, command := range []string{"printf LOCAL_BASH_REAL_A > '" + path + "'; printf 'LOCAL_BASH_REAL_A\\nLOCAL_BASH_REAL_B <&>\\n'", "printf 'LOCAL_BASH_EXPECTED_FAILURE <&>\\n' >&2; exit 7"} {
		if e := session.Submit(context.Background(), tui.Input{TurnID: []string{"local-success", "local-failure"}[i], Text: command, Mode: "bash"}, emit); e != nil {
			t.Fatal(e)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("user shell unexpectedly queried a model", requests.Load())
	}
	physical, e := os.ReadFile(path)
	if e != nil || string(physical) != "LOCAL_BASH_REAL_A" {
		t.Fatal("actual shell artifact", string(physical), e)
	}
	finished := []tui.Event{}
	for _, event := range events {
		if event.Kind == tui.BashFinished {
			finished = append(finished, event)
		}
	}
	if len(finished) != 2 || !strings.Contains(finished[0].Text, "LOCAL_BASH_REAL_A\nLOCAL_BASH_REAL_B <&>") || !strings.Contains(finished[1].Text, "LOCAL_BASH_EXPECTED_FAILURE <&>") || !finished[1].IsError {
		t.Fatalf("local Bash output missing %+v", finished)
	}
	if e := session.Submit(context.Background(), tui.Input{TurnID: "after-shell", Text: "Continue after the local command", Mode: "prompt"}, nil); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 {
		t.Fatal("ordinary SDK input no longer reaches same Harness", requests.Load())
	}
	if e := session.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(o.Native.Home, ".claude", "projects", "*", "*.jsonl"))
	commands, caveats := 0, 0
	for _, file := range files {
		body, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if line == "" {
				continue
			}
			row := obj(json.RawMessage(line))
			message, _ := row.Object("message")
			text := message.String("content")
			if strings.HasPrefix(text, "<bash-input>") {
				commands++
			}
			if strings.HasPrefix(text, "<local-command-caveat>") {
				caveats++
			}
		}
	}
	if commands != 2 || caveats != 2 {
		t.Fatal("actual canonical shell producer not persisted", commands, caveats, files)
	}
}

func TestTerminalBashRejectsForeignOwnerBeforeRunningOrInstallingInputProducer(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	foreign := cooperative.New()
	defer foreign.Close()
	called := false
	e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		return s.process.Host.RunTerminalBash(scope, context.Background(), protocol.NewJSONObject(), func(*cooperative.Scope, context.Context, json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
			called = true
			return nil, nil
		})
	})
	if !errors.Is(e, cooperative.ErrRestricted) || called {
		t.Fatal("foreign owner bypassed shell admission", e, called)
	}
	if _, e = s.process.RunTerminalBash(context.Background(), "printf OWNED_SHELL_AFTER_FOREIGN_REJECTION", nil); e != nil {
		t.Fatal("foreign caller poisoned terminal mode", e)
	}
}

func TestTerminalBashCancelJoinsPhysicalShellAndLeavesPromptUsable(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.ForegroundShellProgress = true
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	started := filepath.Join(o.WorkDir, "started")
	pidfile := filepath.Join(o.WorkDir, "pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Submit(ctx, tui.Input{TurnID: "cancel-shell", Text: "printf '%s' $$ > '" + pidfile + "'; printf started > '" + started + "'; while true; do sleep 1; done", Mode: "bash"}, nil)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(started); e == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("real shell never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancelled physical shell did not settle")
	}
	data, e := os.ReadFile(pidfile)
	if e != nil {
		t.Fatal(e)
	}
	pid, _ := strconv.Atoi(string(data))
	process, e := os.FindProcess(pid)
	if e == nil && process.Signal(syscall.Signal(0)) == nil {
		t.Fatal("physical shell leaked after cancellation", pid)
	}
	if _, e = s.process.RunTerminalBash(context.Background(), "printf ACTUAL_SHELL_RECOVERY", nil); e != nil {
		t.Fatal("local mode retained cancellation", e)
	}
}

func TestTerminalBashUnavailablePowerShellPreferenceUsesOriginalMacFallback(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"defaultShell":"powershell"}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	result, e := s.process.RunTerminalBash(context.Background(), "printf ORIGINAL_MAC_SHELL_FALLBACK", nil)
	if e != nil || result.Stdout != "ORIGINAL_MAC_SHELL_FALLBACK" {
		t.Fatal("Source falls back to available Bash", result.Stdout, e)
	}
}

func TestOrdinarySDKCannotSelectTerminalBashByInjectingMode(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	marker := filepath.Join(o.WorkDir, "injected-shell")
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "mode", Value: raw("bash")}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"role": "user", "content": "printf injected > '" + marker + "'"})})
	e = s.SubmitSDK(context.Background(), user)
	if e == nil || !strings.Contains(e.Error(), "SDK field outside original plain-text profile: mode") {
		t.Fatal("external SDK injected a trusted terminal mode", e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("injected shell executed", e)
	}
}
