package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestActualSourcePermissionDecisionDebugRenderer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	contextRules := map[string]any{"alwaysAllowRules": map[string]any{"userSettings": []string{"Bash(npm:*)", "PowerShell(Write-Output:*)", "Read(src/**)"}}, "alwaysAskRules": map[string]any{"projectSettings": []string{"Bash"}}, "alwaysDenyRules": map[string]any{"policySettings": []string{"PowerShell"}}}
	addRules := map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "npm:*"}, map[string]any{"toolName": "Read", "ruleContent": "src/**"}}, "behavior": "allow", "destination": "session"}
	results := []map[string]any{
		{"behavior": "ask", "message": "Review the actual command."},
		{"behavior": "allow", "message": "This is hidden for allow", "decisionReason": map[string]any{"type": "mode", "mode": "bypassPermissions"}},
		{"behavior": "deny", "message": "Denied by rule", "decisionReason": map[string]any{"type": "rule", "rule": map[string]any{"source": "policySettings", "ruleValue": map[string]any{"toolName": "PowerShell", "ruleContent": "Write-Output(中文🙂)"}}}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "sandboxOverride"}, "suggestions": []any{addRules}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "workingDir", "reason": "outside the working directory: 中文🙂/a very long folder name"}, "suggestions": []any{map[string]any{"type": "addDirectories", "directories": []string{"/owned/work 中文🙂", "/owned/other"}}, map[string]any{"type": "setMode", "mode": "plan"}, map[string]any{"type": "setMode", "mode": "acceptEdits"}}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "safetyCheck", "reason": "Requires approval before running."}, "suggestions": []any{map[string]any{"type": "removeRules", "rules": []any{map[string]any{"toolName": "Bash"}}}}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "permissionPromptTool", "permissionPromptToolName": "mcp__owned__approval"}, "suggestions": []any{}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "hook", "hookName": "PermissionRequest", "reason": "Owned hook reason"}, "suggestions": []any{addRules, map[string]any{"type": "addDirectories", "directories": []string{"/owned/中文🙂"}}, map[string]any{"type": "setMode", "mode": "dontAsk"}}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "hook", "hookName": "PreToolUse"}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "asyncAgent", "reason": "Background policy assessment."}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "classifier", "classifier": "transcript", "reason": "Needs a manual check."}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "unknown"}},
		{"behavior": "ask", "decisionReason": map[string]any{"type": "subcommandResults", "entries": []any{
			[]any{"echo 'owned 中文🙂'", map[string]any{"behavior": "allow", "decisionReason": map[string]any{"type": "mode", "mode": "default"}}},
			[]any{"npm install", map[string]any{"behavior": "ask", "decisionReason": map[string]any{"type": "other", "reason": "Review this command."}, "suggestions": []any{addRules}}},
			[]any{"rm private.txt", map[string]any{"behavior": "deny", "decisionReason": map[string]any{"type": "subcommandResults", "entries": []any{}}}},
		}}},
	}
	rows := []map[string]any{}
	requests := []PermissionRequest{}
	widths := []int{}
	for _, width := range []int{100, 47, 24} {
		for index, result := range results {
			tool := "Bash"
			if index%2 == 1 {
				tool = "PowerShell"
			}
			rows = append(rows, map[string]any{"result": result, "context": contextRules, "toolName": tool, "sandboxAutoAllow": index%3 == 0, "width": width})
			encodedResult, _ := json.Marshal(result)
			encodedContext, _ := json.Marshal(contextRules)
			requests = append(requests, PermissionRequest{ID: "actual", ToolName: tool, Debug: &PermissionDebugData{Result: encodedResult, Context: encodedContext, SandboxAutoAllow: index%3 == 0}})
			widths = append(widths, width)
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-permission-debug.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_DEBUG_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_ESSENTIAL=1", "ANTHROPIC_API_KEY=fake-permission-debug-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:9"}
	input, _ := json.Marshal(map[string]any{"render": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual debug renderer", err, stderr.String())
	}
	var source struct{ Renders []string }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Renders) != len(rows) {
		t.Fatal("actual debug render receipts", err, len(source.Renders), len(rows))
	}
	plain := func(value string) string {
		lines := strings.Split(ansi.Strip(value), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.TrimRight(strings.Join(lines, "\n"), "\n")
	}
	for index, request := range requests {
		before, _ := json.Marshal(request.Debug)
		actual := strings.Join(PermissionDebugLines(request, widths[index]), "\n")
		if plain(actual) != plain(source.Renders[index]) {
			t.Errorf("actual Source debug row%d width%d differs\nGo:\n%s\nSource:\n%s", index, widths[index], plain(actual), plain(source.Renders[index]))
		}
		after, _ := json.Marshal(request.Debug)
		if !bytes.Equal(before, after) {
			t.Fatal("debug renderer mutated permission", index)
		}
	}
}
