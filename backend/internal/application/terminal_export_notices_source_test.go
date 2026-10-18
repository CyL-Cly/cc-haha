package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/ansi"
)

func TestActualSourceExportSixNoticePredicatesAndRendering(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:9")
	home, cwd := o.Environment["HOME"], o.WorkDir
	type noticeCase struct {
		name  string
		input map[string]any
		value tui.ExportStatusContext
		ids   []string
	}
	cases := []noticeCase{
		{"none", nil, tui.ExportStatusContext{}, nil},
		{"memory-edge", map[string]any{"memoryFiles": []any{map[string]any{"path": filepath.Join(cwd, "CLAUDE.md"), "content": strings.Repeat("x", 40000)}}}, tui.ExportStatusContext{Memory: []tui.ExportMemoryNotice{{Path: filepath.Join(cwd, "CLAUDE.md"), Characters: 40000}}}, nil},
		{"large-memory", map[string]any{"memoryFiles": []any{map[string]any{"path": filepath.Join(cwd, "CLAUDE.md"), "content": strings.Repeat("x", 40001)}}}, tui.ExportStatusContext{Memory: []tui.ExportMemoryNotice{{Path: filepath.Join(cwd, "CLAUDE.md"), Characters: 40001}}}, []string{"large-memory-files"}},
		{"large-agents", map[string]any{"agentDefinitions": map[string]any{"activeAgents": []any{map[string]any{"source": "userSettings", "agentType": "actual-agent", "whenToUse": strings.Repeat("x", 70000)}}}}, tui.ExportStatusContext{}, []string{"large-agent-descriptions"}},
		{"subscriber-token", map[string]any{"subscriber": true, "authTokenSource": "ANTHROPIC_AUTH_TOKEN"}, tui.ExportStatusContext{Subscriber: true, AuthTokenSource: "ANTHROPIC_AUTH_TOKEN"}, []string{"claude-ai-external-token"}},
		{"subscriber-helper", map[string]any{"subscriber": true, "authTokenSource": "apiKeyHelper"}, tui.ExportStatusContext{Subscriber: true, AuthTokenSource: "apiKeyHelper"}, []string{"claude-ai-external-token"}},
		{"stored-env", map[string]any{"storedAPIKey": true, "apiKeySource": "ANTHROPIC_API_KEY"}, tui.ExportStatusContext{StoredAPIKey: true, APIKeySource: "ANTHROPIC_API_KEY"}, []string{"api-key-conflict"}},
		{"stored-helper", map[string]any{"storedAPIKey": true, "apiKeySource": "apiKeyHelper"}, tui.ExportStatusContext{StoredAPIKey: true, APIKeySource: "apiKeyHelper"}, []string{"api-key-conflict"}},
		{"both-env", map[string]any{"authTokenSource": "ANTHROPIC_AUTH_TOKEN", "apiKeySource": "ANTHROPIC_API_KEY"}, tui.ExportStatusContext{AuthTokenSource: "ANTHROPIC_AUTH_TOKEN", APIKeySource: "ANTHROPIC_API_KEY"}, []string{"both-auth-methods"}},
		{"both-managed", map[string]any{"authTokenSource": "claude.ai", "apiKeySource": "/login managed key"}, tui.ExportStatusContext{AuthTokenSource: "claude.ai", APIKeySource: "/login managed key"}, []string{"both-auth-methods"}},
		{"same-helper", map[string]any{"authTokenSource": "apiKeyHelper", "apiKeySource": "apiKeyHelper"}, tui.ExportStatusContext{AuthTokenSource: "apiKeyHelper", APIKeySource: "apiKeyHelper"}, nil},
		{"jetbrains-missing", map[string]any{"jetbrainsIDE": "IntelliJ IDEA", "autoInstallIDE": true}, tui.ExportStatusContext{JetBrainsIDE: "IntelliJ IDEA", AutoInstallIDE: true}, []string{"jetbrains-plugin-install"}},
		{"jetbrains-installed", map[string]any{"jetbrainsIDE": "IntelliJ IDEA", "autoInstallIDE": true, "jetbrainsInstalled": true}, tui.ExportStatusContext{JetBrainsIDE: "IntelliJ IDEA", AutoInstallIDE: true, JetBrainsInstalled: true}, nil},
		{"jetbrains-disabled", map[string]any{"jetbrainsIDE": "IntelliJ IDEA", "autoInstallIDE": false}, tui.ExportStatusContext{JetBrainsIDE: "IntelliJ IDEA"}, nil},
	}
	rows := []any{}
	for i := range cases {
		if cases[i].input == nil {
			cases[i].input = map[string]any{}
		}
		cases[i].input["cwd"] = cwd
		cases[i].value.CWD = cwd
		rows = append(rows, cases[i].input)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-export-notices.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + o.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + home, "PATH=/usr/bin:/bin", "TZ=UTC", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_EXPORT_NOTICES_FIXTURE=1"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	output, err := command.CombinedOutput()
	if err := os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "session-source-notices.json"), output, 0600); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Results []struct {
			IDs                    []string
			Text                   string
			AgentDescriptionTokens float64
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(cases) || len(source.Caps.Blocked)+len(source.Caps.SecureReads) != 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			original := source.Results[i]
			if len(original.IDs) != len(row.ids) || len(row.ids) > 0 && !reflect.DeepEqual(original.IDs, row.ids) {
				t.Fatalf("Source gates: %v want %v", original.IDs, row.ids)
			}
			row.value.AgentDescriptionTokens = original.AgentDescriptionTokens
			got := strings.Join(tui.ExportStatusNoticeLines(row.value, 80), "\n")
			want := strings.TrimRight(ansi.Strip(original.Text), "\n")
			if got != want {
				t.Fatalf("Go:\n%s\nSource:\n%s", got, want)
			}
		})
	}
}
