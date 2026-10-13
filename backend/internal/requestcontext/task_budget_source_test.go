package requestcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceTaskBudgetProviderExtraBodyRemainingAndBetaOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	total, remaining := 8192., 0.
	rows := []struct {
		Env    map[string]string
		Budget *TaskBudget
		Output protocol.JSONObject
		Betas  []string
	}{
		{Env: map[string]string{}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com/path"}, Budget: &TaskBudget{Total: total, Remaining: &remaining}, Betas: []string{"effort-2025-11-24"}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:443"}, Budget: &TaskBudget{Total: total}, Betas: []string{TaskBudgetBeta, TaskBudgetBeta}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:444"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://relay.invalid", "CLAUDE_CODE_USE_FOUNDRY": "yes"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "CLAUDE_CODE_USE_BEDROCK": "1"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"CLAUDE_CODE_USE_AZURE_OPENAI": "1"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api-staging.anthropic.com", "USER_TYPE": "ant"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api-staging.anthropic.com", "USER_TYPE": "external"}, Budget: &TaskBudget{Total: total}},
		{Env: map[string]string{}, Budget: &TaskBudget{Total: total}, Output: protocol.NewJSONObject(protocol.JSONField{Name: "task_budget", Value: []byte("null")}, protocol.JSONField{Name: "future", Value: []byte("true")})},
		{Env: map[string]string{}, Output: protocol.NewJSONObject(protocol.JSONField{Name: "effort", Value: []byte(`"high"`)})},
	}
	inputRows := []map[string]any{}
	gotRows := []map[string]any{}
	for _, row := range rows {
		output := row.Output
		if !output.IsObject() {
			output = protocol.NewJSONObject()
		}
		betas := append([]string{}, row.Betas...)
		inputRows = append(inputRows, map[string]any{"env": row.Env, "budget": row.Budget, "output": output.Clone(), "betas": betas})
		betas = configureTaskBudget(Context{Host: Host{Env: row.Env}, Options: Options{TaskBudget: row.Budget}}, &output, betas)
		gotRows = append(gotRows, map[string]any{"output": output, "betas": betas})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/native-task-budget.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_BUDGET_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "ANTHROPIC_API_KEY=fake-task-budget-key", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": inputRows})
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Rows         []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	var want, got any
	encoded, _ := json.Marshal(source.Rows)
	_ = json.Unmarshal(encoded, &want)
	encoded, _ = json.Marshal(gotRows)
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Source%s Go%s", out, encoded)
	}
}
