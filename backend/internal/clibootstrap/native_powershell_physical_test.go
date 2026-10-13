package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

// Both producers execute the installed PowerShell binary. The Go caller is the
// actual MainPrint tool service, NativeBinding and cwd consumer; the Source
// caller is the original PowerShellTool.call, mapper and ShellCommand.
func TestActualSourceAndNativeMainPrintPowerShellPhysicalCommandsAndCwd(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	const executable = "/opt/homebrew/bin/pwsh"
	if _, err := os.Stat(executable); err != nil {
		t.Skip("actual PowerShell is unavailable")
	}
	var root string
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		root = n.CWD
		n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		n.Environment["TMPDIR"] = n.Temp
		n.Environment["NO_COLOR"] = "1"
		n.Environment["TERM"] = "dumb"
	}, func(options *MainPrintOptions) {
		options.NativeShells = windowshell.NewNative(options.Platform, options.Native.CWD, options.Native.Environment)
		options.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	})
	rows := []map[string]any{}
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jBuoAAAAASUVORK5CYII="
	for index, command := range []string{"Write-Output 'actual PS output 中文🙂'", "[Console]::Error.WriteLine('ACTUAL_PS_STDERR_ONLY')", "Set-Content -LiteralPath 'actual-ps-write.txt' -NoNewline -Value 'ACTUAL_PS_PHYSICAL_中文🙂'; Get-Content -LiteralPath 'actual-ps-write.txt'", "& /usr/bin/false", "function rg { $global:LASTEXITCODE = 1 }; rg", "function robocopy { $global:LASTEXITCODE = 3 }; robocopy", "function findstr { $global:LASTEXITCODE = 2 }; findstr", "New-Item -ItemType Directory -Force './actual-ps-subdir' | Out-Null; Set-Location './actual-ps-subdir'; Write-Output 'ACTUAL_PS_CWD_CHANGE'", "Write-Output '" + image + "'", "Write-Output '" + image + "'; Write-Output 'EXTRA_IMAGE_TEXT'", "Write-Output 'data:image/png;base64,'"} {
		rows = append(rows, map[string]any{"input": map[string]any{"command": command}, "id": "actual-ps-tool-" + string(rune('0'+index))})
	}
	for _, kind := range []string{"interrupt", "stop", "explicit-background"} {
		started := filepath.Join(root, "ps-"+kind+"-started")
		command := "Write-Output 'PS_LIFECYCLE_BEFORE'; Set-Content -LiteralPath '" + started + "' -NoNewline -Value 'STARTED'; Start-Sleep -Seconds 3; Add-Content -LiteralPath '" + started + "' -NoNewline -Value '_FINISHED'; Write-Output 'PS_LIFECYCLE_AFTER'"
		row := map[string]any{"input": map[string]any{"command": command}, "id": "ps-physical-" + kind, "joinBackground": kind != "stop"}
		if kind == "explicit-background" {
			row["input"].(map[string]any)["run_in_background"] = true
		} else {
			row["abortAfterFile"], row["abortReason"] = started, kind
		}
		rows = append(rows, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	n := m.options.Native
	command.Dir = n.Home
	command.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CLAUDE_CODE_TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-physical-powershell", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_POWERSHELL_PATH=" + executable, "NO_COLOR=1", "TERM=dumb", "PATH=/usr/bin:/bin"}
	input := rawMain(map[string]any{"home": n.Home, "cwd": root, "sessionId": m.Controller.SessionID(), "execute": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual Source PowerShell call", err, stderr.String())
	}
	var source struct{ Executions []protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Executions) != len(rows) {
		t.Fatal(err, string(output))
	}
	for index, row := range rows {
		var got protocol.JSONObject
		callContext, stopCall := context.WithCancelCause(ctx)
		started, _ := row["abortAfterFile"].(string)
		if started != "" {
			if err := os.Remove(started); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			go func() {
				timer := time.NewTicker(20 * time.Millisecond)
				defer timer.Stop()
				for {
					select {
					case <-callContext.Done():
						return
					case <-timer.C:
						if _, err := os.Stat(started); err == nil {
							if row["abortReason"] == "interrupt" {
								stopCall(queryloop.SubmitInterrupt)
							} else {
								stopCall(context.Canceled)
							}
							return
						}
					}
				}
			}()
		}
		err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if err := m.nativeBashCwdChanged(scope, m.state.Snapshot().CWD, root); err != nil {
				return err
			}
			value := taskObject(t, rawMain(row["input"]))
			data, failure := m.nativeTools.Call(callContext, "PowerShell", value, scope, false)
			got = protocol.NewJSONObject()
			if failure == nil {
				got.Set("data", data)
				mapped, err := m.nativeTools.MapTerminalPowerShellResult(scope, data, rawMain(row["id"]))
				if err != nil {
					return err
				}
				got.Set("mapped", rawMain(mapped))
				taskData := taskObject(t, data)
				if taskID := taskData.String("backgroundTaskId"); taskID != "" && row["joinBackground"] == true {
					task, err := m.Controller.BackgroundTaskOnOwner(scope, taskID)
					if err != nil || task == nil {
						return err
					}
					joined, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, task.Join(ctx) })
					if err != nil {
						return err
					}
					if _, err = joined.Await(scope); err != nil {
						return err
					}
					completed, err := m.Controller.BackgroundTaskOnOwner(scope, taskID)
					if err != nil || completed == nil {
						return err
					}
					outcome, _ := completed.Data.Object("result")
					got.Set("background", rawMain(map[string]any{"type": completed.Data.String("type"), "isBackgrounded": string(completed.Data.Get("isBackgrounded")) == "true", "code": outcome.Get("code"), "interrupted": outcome.Get("interrupted")}))
				}
			} else {
				var shell *toolerrors.ShellError
				if !errors.As(failure, &shell) {
					return failure
				}
				got.Set("error", rawMain(map[string]any{"stdout": shell.Stdout, "stderr": shell.Stderr, "code": shell.Code, "interrupted": shell.Interrupted}))
			}
			got.Set("cwd", quoteMain(m.state.Snapshot().CWD))
			return nil
		})
		stopCall(context.Canceled)
		if err != nil {
			t.Fatal(index, err)
		}
		if started != "" {
			physical, err := os.ReadFile(started)
			if err != nil {
				t.Fatal(err)
			}
			if row["abortReason"] == "interrupt" && string(physical) != "STARTED_FINISHED" {
				t.Fatal("submit interrupt killed or replayed real process", string(physical))
			}
			if row["abortReason"] == "stop" && string(physical) != "STARTED" {
				t.Fatal("Stop allowed late physical side effect", string(physical))
			}
		}
		want := source.Executions[index].Clone()
		if failure, exists := want.Object("error"); exists {
			failure.Delete("name")
			failure.Delete("message")
			want.Set("error", rawMain(failure))
		}
		for _, object := range []*protocol.JSONObject{&got, &want} {
			if data, ok := object.Object("data"); ok && data.String("backgroundTaskId") != "" {
				taskID := data.String("backgroundTaskId")
				data.Set("backgroundTaskId", quoteMain("PHYSICAL_TASK_ID"))
				object.Set("data", rawMain(data))
				mapped, _ := object.Object("mapped")
				mapped.Set("content", quoteMain(strings.ReplaceAll(mapped.String("content"), taskID, "PHYSICAL_TASK_ID")))
				object.Set("mapped", rawMain(mapped))
			}
		}
		var left, right any
		_ = json.Unmarshal(rawMain(got), &left)
		_ = json.Unmarshal(rawMain(want), &right)
		if !reflect.DeepEqual(left, right) {
			t.Errorf("actual physical PS row%d\nGo%s\nSource%s", index, rawMain(got), rawMain(want))
		}
	}
	physical, err := os.ReadFile(filepath.Join(root, "actual-ps-write.txt"))
	if err != nil || string(physical) != "ACTUAL_PS_PHYSICAL_中文🙂" {
		t.Fatal("real PowerShell did not produce expected physical bytes", string(physical), err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
