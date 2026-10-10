package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestStaticGitInspectionCommandRulesAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Command string         `json:"command"`
		Context map[string]any `json:"context"`
	}
	rows := []row{}
	for _, cmd := range []string{"git status", "git diff --name-only origin/HEAD...", "git log --no-decorate origin/HEAD...", "git diff origin/HEAD..."} {
		for _, behavior := range []string{"Deny", "Ask", "Allow"} {
			rows = append(rows, row{cmd, map[string]any{"mode": "default", "always" + behavior + "Rules": map[string]any{"command": []string{"Bash(git:*)"}}}})
		}
	}
	rows = append(rows, row{"git status", map[string]any{"mode": "default", "alwaysDenyRules": map[string]any{"session": []string{"Bash(git status)"}}, "alwaysAllowRules": map[string]any{"command": []string{"Bash(git:*)"}}}}, row{"git status", map[string]any{"mode": "default", "alwaysAskRules": map[string]any{"session": []string{"Bash(git status:*)"}}, "alwaysAllowRules": map[string]any{"command": []string{"Bash(git:*)"}}}}, row{"git status", map[string]any{"mode": "default", "alwaysDenyRules": map[string]any{"session": []string{"Bash(git stat:*)"}}, "alwaysAllowRules": map[string]any{"command": []string{"Bash(git status)"}}}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/bash-git-rules.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BASH_GIT_RULE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(raw))
	}
	var source struct{ Rows []string }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err, string(raw))
	}
	for i, row := range rows {
		data, _ := json.Marshal(row.Context)
		var context protocol.JSONObject
		_ = context.UnmarshalJSON(data)
		input := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: json.RawMessage(quote(row.Command))})
		got, err := (&Service{}).bashGitRuleDecision(context, input)
		if err != nil || got != source.Rows[i] {
			t.Errorf("%s %v Go%s/%v Source%s", row.Command, row.Context, got, err, source.Rows[i])
		}
	}
}
func TestGitInspectionRuleConsumerRetainsUnimplementedSyntaxGuard(t *testing.T) {
	for _, command := range []string{"git status; touch other", "git status && curl attacker.invalid", "git diff --no-index /outside/a /outside/b", "git -c alias.status='!code' status", "PATH=/outside git status", "git status > output", "git status\nrm file", "git status $(command)", "'git' status", "git commit -m text"} {
		if staticGitInspection(command) {
			t.Errorf("unimplemented AST admitted: %q", command)
		}
	}
}
