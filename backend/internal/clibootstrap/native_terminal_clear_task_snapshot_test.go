package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// This branch is launched by the actual configured SessionEnd command. It
// forwards its actual stdin to an owned HTTP barrier, then exits without a
// testing-framework PASS line. It has no MainPrint, provider, or task producer.
func terminalClearHookBarrierProcess(address string) int {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() == "" || u.Path != "/owned-clear-hook" {
		fmt.Fprintln(os.Stderr, "invalid owned clear hook address", err)
		return 2
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		fmt.Fprintln(os.Stderr, "clear hook requires a literal loopback peer")
		return 2
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil || len(input) > 1<<20 {
		fmt.Fprintln(os.Stderr, "clear hook stdin", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(input))
	if err != nil {
		fmt.Fprintln(os.Stderr, "clear hook request", err)
		return 2
	}
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clear hook barrier", err)
		return 2
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		fmt.Fprintln(os.Stderr, "clear hook barrier status", response.StatusCode)
		return 2
	}
	return 0
}

func TestActualTerminalClearKeepsAgentCompletedDuringSessionEndOutput(t *testing.T) {
	const helperMarker = "--cc-haha-owned-clear-sessionend-barrier"
	for i, argument := range os.Args {
		if argument == helperMarker {
			if i+2 != len(os.Args) {
				os.Exit(2)
			}
			os.Exit(terminalClearHookBarrierProcess(os.Args[i+1]))
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("this physical command-hook/output-symlink fixture requires the existing Unix shell path")
	}
	const childPrompt = "ACTUAL_CLEAR_CHILD_HELD_AT_SESSION_END_C9E4"
	const childResult = "ACTUAL_CLEAR_CHILD_COMPLETED_BEFORE_HOOK_RETURN_C9E4"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	modelEntered := make(chan struct{}, 1)
	hookEntered := make(chan []byte, 1)
	modelRelease, hookRelease := make(chan struct{}), make(chan struct{})
	var modelOnce, hookOnce sync.Once
	releaseModel := func() { modelOnce.Do(func() { close(modelRelease) }) }
	releaseHook := func() { hookOnce.Do(func() { close(hookRelease) }) }
	var modelCalls, hookCalls, hookResponses, hookAborts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error("owned clear peer expected an actual POST", r.Method)
			http.Error(w, "unexpected fixture method", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			t.Error("owned clear peer request body", err)
			http.Error(w, "invalid fixture input", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/owned-clear-hook" {
			if hookCalls.Add(1) != 1 {
				t.Error("clear command hook entered more than once")
				http.Error(w, "duplicate fixture hook", http.StatusConflict)
				return
			}
			hookEntered <- append([]byte(nil), body...)
			select {
			case <-hookRelease:
				w.WriteHeader(http.StatusNoContent)
				hookResponses.Add(1)
			case <-r.Context().Done():
				hookAborts.Add(1)
			}
			return
		}
		if r.URL.Path != "/v1/messages" || modelCalls.Add(1) != 1 || !bytes.Contains(body, []byte(childPrompt)) {
			t.Error("unexpected owned clear model request", r.URL.Path, string(body))
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		modelEntered <- struct{}{}
		select {
		case <-modelRelease:
			emitPlanFixture(w, 1, "", childResult)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		releaseModel()
		releaseHook()
		cancel()
		peer.CloseClientConnections()
		peer.Close()
	})
	f := newNativePluginFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	command := quote(executable) + " -test.run " + quote("^TestActualTerminalClearKeepsAgentCompletedDuringSessionEndOutput$") + " -test.count=1 -- " + helperMarker + " " + quote(peer.URL+"/owned-clear-hook")
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{
		"enabledPlugins": map[string]bool{"sample@mkt": false},
		"hooks": map[string]any{"SessionEnd": []any{map[string]any{
			"matcher": "clear", "hooks": []any{map[string]any{"type": "command", "command": command}},
		}}},
	})))
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "owned-clear-loopback-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS"] = "15000"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	seedNativeReleaseWarmFixture(t, f, "fixture")
	type clearResult struct {
		value protocol.JSONObject
		err   error
	}
	var m *MainPrint
	var clearDone chan clearResult
	clearJoined := false
	t.Cleanup(func() {
		releaseModel()
		releaseHook()
		if clearDone != nil && !clearJoined {
			select {
			case <-clearDone:
				clearJoined = true
			case <-time.After(16 * time.Second):
				t.Error("actual TerminalClear did not join after releasing both owned barriers")
			}
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if m != nil {
			if err := m.ShutdownSession(cleanupCtx); err != nil {
				t.Error("actual clear fixture shutdown", err)
			}
		}
	})
	disabled := false
	var diagnostics inProcessDiagnosticBuffer
	m, err = NewMainPrint(MainPrintOptions{
		Interactive: true, ForegroundShellProgress: true, Native: f.n,
		PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS,
		Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics,
		Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "owned actual clear parent", Tools: []string{"Agent,Bash,TaskOutput,TaskStop,Write,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	oldSID := m.Controller.SessionID()
	var launched builtintools.AgentRunResult
	var join func(context.Context) error
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, m, childPrompt)
		if err != nil {
			return err
		}
		future, err := m.runBackgroundAgent(ctx, scope, request)
		if err != nil {
			return err
		}
		launched, err = future.Await(scope)
		if err != nil {
			return err
		}
		task, err := m.Controller.BackgroundTaskOnOwner(scope, launched.AgentID)
		if err != nil {
			return err
		}
		if task == nil || task.Join == nil || task.Data.String("status") != "running" || string(task.Data.Get("isBackgrounded")) != "true" {
			return fmt.Errorf("actual child was not registered as a joinable running background task")
		}
		join = task.Join
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-modelEntered:
	case <-ctx.Done():
		t.Fatal("real background Agent did not enter its held HTTP request", ctx.Err())
	}
	oldTarget, err := os.Readlink(launched.OutputFile)
	if err != nil || !strings.Contains(oldTarget, string(filepath.Separator)+oldSID+string(filepath.Separator)) {
		t.Fatal("actual child output symlink was not tied to the original SID", oldTarget, err)
	}
	clearDone = make(chan clearResult, 1)
	go func() {
		value, err := m.TerminalClear(ctx)
		clearDone <- clearResult{value: value, err: err}
	}()
	var hookInput []byte
	select {
	case hookInput = <-hookEntered:
	case <-ctx.Done():
		t.Fatal("actual configured SessionEnd command did not enter its owned barrier", ctx.Err())
	}
	var hook protocol.JSONObject
	if err = hook.UnmarshalJSON(hookInput); err != nil || hook.String("reason") != "clear" || hook.String("hook_event_name") != "SessionEnd" || hook.String("session_id") != oldSID {
		t.Fatal("actual SessionEnd command stdin contract", err, string(hookInput))
	}
	select {
	case result := <-clearDone:
		clearJoined = true
		t.Fatal("clear returned while the real SessionEnd command was held", result.err)
	default:
	}
	releaseModel()
	if err = join(ctx); err != nil {
		t.Fatal("actual background Agent did not complete/join inside the hook window", err)
	}
	// The original TerminalClear stack is parked in the actual hook. Read is an
	// admitted callback; another Owner.Run would incorrectly start a second ASK.
	read, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		task, err := m.Controller.BackgroundTaskOnOwner(scope, launched.AgentID)
		if err != nil {
			return err
		}
		if task == nil || task.Data.String("status") != "completed" || task.Data.String("outputFile") != launched.OutputFile || m.Controller.SessionID() != oldSID {
			return fmt.Errorf("real child did not become completed before SessionEnd returned")
		}
		return nil
	})
	if err == nil {
		err = read.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	completedBytes, err := os.ReadFile(launched.OutputFile)
	if err != nil || !bytes.Contains(completedBytes, []byte(childResult)) || !bytes.Contains(completedBytes, []byte(`"isSidechain":true`)) {
		t.Fatal("real completed sidechain was not readable before releasing SessionEnd", err, string(completedBytes))
	}
	releaseHook()
	var cleared clearResult
	select {
	case cleared = <-clearDone:
		clearJoined = true
	case <-ctx.Done():
		t.Fatal("actual clear did not return after the real command hook completed", ctx.Err())
	}
	if cleared.err != nil || cleared.value.String("sessionId") == "" || cleared.value.String("sessionId") == oldSID || cleared.value.String("sessionId") != m.Controller.SessionID() {
		t.Fatal("actual clear failed to regenerate the real session", cleared.err, rawMain(cleared.value))
	}
	if hookResponses.Load() != 1 || hookAborts.Load() != 0 || strings.Contains(diagnostics.String(), "SessionEnd hook [") {
		t.Fatal("real SessionEnd command did not finish successfully", hookResponses.Load(), hookAborts.Load(), diagnostics.String())
	}
	t.Logf("actual held SessionEnd command completed: responses=%d aborts=%d failureDiagnostic=false", hookResponses.Load(), hookAborts.Load())
	newTarget, linkErr := os.Readlink(launched.OutputFile)
	if linkErr != nil || newTarget != oldTarget {
		t.Errorf("completed-during-SessionEnd Agent output was incorrectly retargeted after clear: before=%q after=%q error=%v", oldTarget, newTarget, linkErr)
	}
	output, outputErr := os.ReadFile(launched.OutputFile)
	if outputErr != nil || !bytes.Equal(output, completedBytes) {
		t.Errorf("completed Agent output must remain readable through its real output symlink after clear: error=%v gotBytes=%d wantBytes=%d", outputErr, len(output), len(completedBytes))
	}
	original, originalErr := os.ReadFile(oldTarget)
	if originalErr != nil || !bytes.Equal(original, completedBytes) {
		t.Error("clear rewrote the original completed child transcript", originalErr)
	}
	if modelCalls.Load() != 1 || hookCalls.Load() != 1 {
		t.Error("clear fixture opened an extra provider or command-hook execution", modelCalls.Load(), hookCalls.Load())
	}
}

func TestActualTerminalClearRejectsFailedResumeBeforeSessionEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this real command-hook journal fixture uses the existing Unix shell path")
	}
	var modelRequests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			modelRequests.Add(1)
			t.Error("failed-resume admission fixture unexpectedly started a model request")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(peer.Close)
	f := newNativePluginFixture(t)
	journal := filepath.Join(f.cwd, "rejected-clear-sessionend-journal.txt")
	const missingResume = "11111111-1111-4111-8111-111111111111"
	const marker = "UNEXPECTED_CLEAR_SESSIONEND_AFTER_FAILED_RESUME_C9E4"
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	command := "printf '%s\\n' " + quote(marker) + " >> " + quote(journal)
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{
		"enabledPlugins": map[string]bool{"sample@mkt": false},
		"hooks": map[string]any{"SessionEnd": []any{map[string]any{
			"matcher": "clear", "hooks": []any{map[string]any{"type": "command", "command": command}},
		}}},
	})))
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "owned-failed-resume-loopback-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	seedNativeReleaseWarmFixture(t, f, "fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	var diagnostics inProcessDiagnosticBuffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{
		Interactive: true, ForegroundShellProgress: true, Native: f.n,
		PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS,
		Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics,
		Arguments: PrintArguments{ComputerUse: &disabled, Resume: missingResume, Model: "fixture-model", SystemPrompt: "owned failed-resume clear admission", Tools: []string{""}, MaxTurns: 1, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()},
	})
	if err != nil {
		t.Fatal("valid real constructor failed before resume activation", err)
	}
	// Match application/session.go's failed-StartSession cleanup: Close, rather
	// than a full ShutdownSession on an uninitialized graph. Observe it finitely.
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { m.Close(); close(closed) }()
		select {
		case <-closed:
			if !m.closed {
				t.Error("failed-resume actual Close returned without closing the graph")
			}
		case <-time.After(10 * time.Second):
			t.Error("failed-resume actual Close did not return within its fixture observation")
		}
	})
	resumeFiles, globErr := filepath.Glob(filepath.Join(f.config, "projects", "*", missingResume+".jsonl"))
	if globErr != nil || len(resumeFiles) != 0 {
		t.Fatal("resume UUID must have no real transcript in the fresh private configuration", globErr, resumeFiles)
	}
	oldSID := m.Controller.SessionID()
	startErr := m.StartSession(ctx)
	if startErr == nil || startErr.Error() != "No conversation found" || m.Host == nil || m.initialized {
		t.Fatal("fixture did not reach the actual failed-resume Host-created/inactive state", startErr, m.Host != nil, m.initialized, diagnostics.String())
	}
	var admissionErr error
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, admissionErr = m.Controller.TerminalClearTasksOnOwner(scope)
		return nil
	}); err != nil || admissionErr != cooperative.ErrRestricted {
		t.Fatal("actual Controller clear admission must reject the failed startup", err, admissionErr)
	}
	if _, err = os.Lstat(journal); !os.IsNotExist(err) {
		t.Fatal("clear-only hook journal existed before the rejected clear", err)
	}
	_, clearErr := m.TerminalClear(ctx)
	if clearErr != cooperative.ErrRestricted {
		t.Error("clear after actual failed resume must retain original admission rejection", clearErr)
	}
	if data, readErr := os.ReadFile(journal); readErr == nil {
		t.Errorf("rejected clear executed the real SessionEnd command before inactive Controller admission: journal=%q", data)
	} else if !os.IsNotExist(readErr) {
		t.Error("owned journal absence could not be observed", readErr)
	}
	if m.Controller.SessionID() != oldSID {
		t.Error("rejected clear changed the actual bootstrap session ID", oldSID, m.Controller.SessionID())
	}
	if modelRequests.Load() != 0 {
		t.Error("failed resume and rejected local clear must not start inference", modelRequests.Load())
	}
	t.Logf("actual failed resume retained Host and Controller rejected clear: startupError=%v modelRequests=%d", startErr, modelRequests.Load())
}
