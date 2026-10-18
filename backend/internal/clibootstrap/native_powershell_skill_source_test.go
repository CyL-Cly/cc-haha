package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestActualSourcePowerShellPromptUtilityUsesPhysicalShellAndStructuredResultFallback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("actual PowerShell required")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		n.Environment["TMPDIR"] = n.Temp
	}, func(options *MainPrintOptions) {
		options.Arguments.PermissionMode = "default"
		options.NativeShells = windowshell.New("windows", options.Native.CWD, options.Native.Environment, windowshell.Dependencies{PathExists: func(context.Context, string) bool { return false }, FileExists: func(_ context.Context, path string) bool { return path == executable }})
		options.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	})
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jBuoAAAAASUVORK5CYII="
	rows := []map[string]any{}
	for _, command := range []string{"Set-Content -LiteralPath 'skill-physical.txt' -NoNewline -Value 'ACTUAL_SKILL_PHYSICAL'; Write-Output '$$ $& 中文🙂'", "[Console]::Error.WriteLine('ACTUAL_SKILL_STDERR_ONLY')", "Write-Output '" + image + "'", "Write-Output 'ACTUAL_SKILL_FAIL_STDOUT'; & /usr/bin/false"} {
		rows = append(rows, map[string]any{"input": map[string]any{"command": command}, "prompt": map[string]any{"text": "before !`" + command + "` after", "name": "/physical", "shell": "powershell"}})
	}
	for _, requested := range []string{"bash", "powershell"} {
		rows = append(rows, map[string]any{"input": map[string]any{"command": ""}, "prompt": map[string]any{"text": "no embedded shell required", "name": "/physical", "shell": requested, "powerShellSetting": "false"}})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	n := m.options.Native
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	child.Dir = n.Home
	child.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CLAUDE_CODE_TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=offline-source-skill-ps", "CLAUDE_CODE_POWERSHELL_PATH=" + executable, "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "cwd": n.CWD, "sessionId": m.Controller.SessionID(), "execute": rows}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal("actual Source prompt execution", err, stderr.String())
	}
	var source struct{ Executions []protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Executions) != len(rows) {
		t.Fatal(err, string(output))
	}
	for index, row := range rows {
		prompt := row["prompt"].(map[string]any)
		request := skillcommands.ShellRequest{Text: prompt["text"].(string), SlashCommand: prompt["name"].(string), Shell: prompt["shell"].(string), AllowedTools: []string{"PowerShell"}}
		current := m
		if index >= 4 {
			current = backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(options *MainPrintOptions) {
				options.Native.Environment["CLAUDE_CODE_USE_POWERSHELL_TOOL"] = "false"
				options.NativeShells = windowshell.New("windows", options.Native.CWD, options.Native.Environment, windowshell.Dependencies{PathExists: func(context.Context, string) bool { return false }, FileExists: func(context.Context, string) bool { return false }})
			})
		}
		var text string
		err = current.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			invocation, err := current.nativeSkillInvocation(scope)
			if err != nil {
				return err
			}
			text, err = invocation.ExecuteShell(ctx, request)
			return err
		})
		if expected, failed := source.Executions[index].Object("error"); failed {
			if err == nil || err.Error() != expected.String("message") {
				t.Fatal("Source prompt error differs", index, err, string(rawMain(expected)))
			}
		} else if err != nil || text != source.Executions[index].String("promptOutput") {
			t.Fatal("Source physical prompt output differs", index, err, text, string(rawMain(source.Executions[index])))
		}
	}
	physical, err := os.ReadFile(filepath.Join(n.CWD, "skill-physical.txt"))
	if err != nil || string(physical) != "ACTUAL_SKILL_PHYSICAL" {
		t.Fatal("skill never reached actual installed PowerShell", string(physical), err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		permission, err := m.nativeToolPermissionContext(scope)
		if err != nil {
			return err
		}
		rules, _ := permission.Object("alwaysAllowRules")
		if rules.Get("command") != nil {
			return fmt.Errorf("temporary skill PowerShell grant leaked: %s", rawMain(permission))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActualAgentPowerShellSkillPresetUsesWorkerCwdAndParentPermission(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("actual PowerShell required")
	}
	parent := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) { n.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable }, func(options *MainPrintOptions) {
		options.NativeShells = windowshell.New("windows", options.Native.CWD, options.Native.Environment, windowshell.Dependencies{PathExists: func(context.Context, string) bool { return false }, FileExists: func(_ context.Context, path string) bool { return path == executable }})
	})
	cwd := filepath.Join(parent.options.Native.Home, "powershell-preset-worker")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	child := presetChildFixture(t, parent, cwd)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		definition, err := skillcommands.Parse(ctx, "---\ndescription: actual PowerShell preset\nshell: powershell\n---\n!`Set-Content -LiteralPath 'preset-cwd.txt' -NoNewline -Value 'ACTUAL_PRESET_PS'; (Get-Location).Path`", "physical", "projectSettings", "", "skills", nil)
		if err != nil {
			return err
		}
		text, err := child.options.DetachedAgent.preset.skillPrompt(ctx, child, scope, definition)
		if err != nil {
			return err
		}
		physical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != physical {
			return fmt.Errorf("PS preset cwd changed: %q != %q", text, physical)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "preset-cwd.txt"))
	if err != nil || string(data) != "ACTUAL_PRESET_PS" {
		t.Fatal("actual child PowerShell filesystem output missing", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(parent.options.Native.CWD, "preset-cwd.txt")); !os.IsNotExist(err) {
		t.Fatal("child skill wrote into parent's working directory", err)
	}
}
