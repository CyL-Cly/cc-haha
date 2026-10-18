package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestActualSourceTerminalPowerShellFallbackPersistsLocalContextForNextHuman(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("actual pwsh required")
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if !bytes.Contains(body, []byte("ACTUAL_TERMINAL_PS_LOCAL_CONTEXT")) || !bytes.Contains(body, []byte("<bash-input>")) {
			t.Error("next real Human lost actual terminal context", string(body))
		}
		emitPlanFixture(w, 1, "", "ACTUAL_PS_NEXT_HUMAN_DONE")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		n.Environment["TMPDIR"] = n.Temp
	}, func(o *MainPrintOptions) {
		o.NativeShells = windowshell.New("windows", o.Native.CWD, o.Native.Environment, windowshell.Dependencies{PathExists: func(context.Context, string) bool { return false }, FileExists: func(_ context.Context, path string) bool { return path == executable }})
	})
	command := "Write-Output 'ACTUAL_TERMINAL_PS_LOCAL_CONTEXT'"
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n := m.options.Native
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	child.Dir = n.Home
	child.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CLAUDE_CODE_TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=offline-source-terminal-ps", "CLAUDE_CODE_POWERSHELL_PATH=" + executable, "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "cwd": n.CWD, "sessionId": m.Controller.SessionID(), "execute": []any{map[string]any{"terminal": true, "input": map[string]any{"command": command}}}}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Executions []protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Executions) != 1 {
		t.Fatal(err, string(output))
	}
	var got protocol.JSONObject
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		processed, result, err := m.nativeProcessTerminalBash(ctx, scope, command, nil)
		if err != nil {
			return err
		}
		if result.Stdout != "ACTUAL_TERMINAL_PS_LOCAL_CONTEXT" {
			t.Fatal("terminal did not route same real PS backend", result)
		}
		messages := []map[string]any{}
		for _, row := range processed.Messages {
			message, _ := row.Object("message")
			messages = append(messages, map[string]any{"type": row.String("type"), "role": message.String("role"), "content": message.Get("content"), "isMeta": string(row.Get("isMeta")) == "true"})
		}
		got = taskObject(t, rawMain(map[string]any{"messages": messages, "shouldQuery": !processed.NoQuery, "cwd": m.state.Snapshot().CWD}))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	_ = json.Unmarshal(rawMain(got), &left)
	_ = json.Unmarshal(rawMain(source.Executions[0]), &right)
	if !reflect.DeepEqual(left, right) {
		t.Fatal("actual terminal Source context differs", string(rawMain(got)), string(rawMain(source.Executions[0])))
	}
	if requests.Load() != 0 {
		t.Fatal("local ! input queried model", requests.Load())
	}
	if _, err = m.RunTerminalBash(ctx, command, nil); err != nil {
		t.Fatal(err)
	}
	if err = m.RunTurn(ctx, taskObject(t, rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Continue with the previous local PowerShell output"}}))); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("terminal replay or duplicate Human model call", requests.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", m.Controller.SessionID()+".jsonl"))
	if len(paths) != 1 {
		t.Fatal("local command canonical transcript absent", paths)
	}
	history, err := os.ReadFile(paths[0])
	count := 0
	for _, line := range bytes.Split(history, []byte{'\n'}) {
		var row protocol.JSONObject
		if row.UnmarshalJSON(line) == nil && row.String("type") == "user" {
			message, _ := row.Object("message")
			if strings.Contains(message.String("content"), "ACTUAL_TERMINAL_PS_LOCAL_CONTEXT") {
				count++
			}
		}
	}
	if err != nil || count != 2 {
		t.Fatal("local input/output not durable once", err, string(history))
	}
}
