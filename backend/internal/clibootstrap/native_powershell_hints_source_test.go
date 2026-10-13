package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestActualSourcePowerShellHintsStripBeforeFailureAndRawWhitespace(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	const executable = "/opt/homebrew/bin/pwsh"
	if _, e := os.Stat(executable); e != nil {
		t.Skip("actual PowerShell required")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		n.Environment["TMPDIR"] = n.Temp
		n.Environment["NO_COLOR"] = "1"
		n.Environment["TERM"] = "dumb"
	}, func(o *MainPrintOptions) {
		o.NativeShells = windowshell.NewNative(o.Platform, o.Native.CWD, o.Native.Environment)
	})
	rows := []map[string]any{}
	for i, command := range []string{"Write-Output '<claude-code-hint v=1 type=plugin value=sample@agent-skills/>'; Write-Output 'VISIBLE'", "Write-Output 'VISIBLE'; Write-Output '<claude-code-hint v=2 type=plugin value=ignored/>'", "Write-Output 'FIRST'; Write-Output '<claude-code-hint v=1 type=plugin value=sample@agent-skills/>'; Write-Output 'LAST'; exit 7", "[Console]::Write(\"`n  leading  `ninner`t`t`nlast  `n`n\")", "Write-Output 'inline <claude-code-hint v=1 type=plugin value=sample@agent-skills/>'"} {
		rows = append(rows, map[string]any{"input": map[string]any{"command": command}, "id": i + 1})
	}
	n := m.options.Native
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = n.Home
	cmd.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CLAUDE_CODE_TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-hints-powershell", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_POWERSHELL_PATH=" + executable, "NO_COLOR=1", "TERM=dumb", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "cwd": n.CWD, "sessionId": m.Controller.SessionID(), "execute": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("Source PS hints", e, stderr.String())
	}
	var source struct{ Executions []protocol.JSONObject }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Executions) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			data, failure := m.nativeTools.Call(ctx, "PowerShell", taskObject(t, rawMain(row["input"])), scope, false)
			if failure == nil {
				want, _ := source.Executions[i].Object("data")
				if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(data), protocol.StringifyValidatedJavaScriptJSON(rawMain(want))) {
					t.Fatalf("PS Source raw call row%d Go %s Source %s", i, data, rawMain(want))
				}
				return nil
			}
			var shell *toolerrors.ShellError
			if !errors.As(failure, &shell) {
				return failure
			}
			want, _ := source.Executions[i].Object("error")
			if shell.Stdout != want.String("stdout") || shell.Stderr != want.String("stderr") || string(rawMain(shell.Code)) != string(want.Get("code")) {
				t.Fatalf("PS Source post-strip failure row%d Go %#v Source %s", i, shell, rawMain(want))
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
}
