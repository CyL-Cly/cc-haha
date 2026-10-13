package querypolicy

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

func TestActualSourceTaskBudgetContextUsesFinalIterationAndSyntheticExclusions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	assistant := func(model string, usage any, text string) protocol.JSONObject {
		encoded, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"model": model, "usage": usage, "content": []map[string]string{{"type": "text", "text": text}}}})
		var record protocol.JSONObject
		_ = record.UnmarshalJSON(encoded)
		return record
	}
	base := assistant("actual-model", map[string]any{"input_tokens": 11, "output_tokens": 3, "cache_read_input_tokens": 999, "cache_creation_input_tokens": 555}, "actual")
	histories := [][]protocol.JSONObject{{}, {base}, {base, assistant("actual-model", map[string]any{"input_tokens": 100, "output_tokens": 30, "iterations": []map[string]int{{"input_tokens": 61, "output_tokens": 7}, {"input_tokens": 17, "output_tokens": 5}}}, "server loop")}, {base, assistant("actual-model", map[string]any{"input_tokens": 8, "output_tokens": 2, "iterations": []any{}}, "empty iterations")}, {base, assistant("actual-model", map[string]any{"input_tokens": 8, "output_tokens": 2, "iterations": nil}, "null iterations")}, {base, assistant("<synthetic>", map[string]any{"input_tokens": 8000, "output_tokens": 99}, "synthetic")}, {base, assistant("actual-model", nil, "missing usage")}, {base, assistant("actual-model", map[string]any{"input_tokens": "12", "output_tokens": -1}, "invalid numeric")}, {base, assistant("actual-model", map[string]any{"iterations": []any{nil}}, "malformed final")}, {base, assistant("actual-model", map[string]any{"iterations": map[string]int{"length": 1}}, "malformed arraylike")}, {base, assistant("actual-model", map[string]any{"iterations": "x"}, "string iterations")}}
	for _, text := range []string{"[Request interrupted by user]", "[Request interrupted by user for tool use]", "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.", "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.", "No response requested."} {
		histories = append(histories, []protocol.JSONObject{base, assistant("actual-model", map[string]any{"input_tokens": 8000, "output_tokens": 99}, text)})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/native-task-budget.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_BUDGET_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "ANTHROPIC_API_KEY=fake-task-budget-key", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"histories": histories})
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Windows      []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	got := []map[string]any{}
	for _, history := range histories {
		value, err := FinalTaskBudgetContext(history)
		if err != nil {
			got = append(got, map[string]any{"error": err.Error()})
		} else {
			got = append(got, map[string]any{"value": value})
		}
	}
	if !reflect.DeepEqual(got, source.Windows) {
		encoded, _ := json.Marshal(got)
		t.Fatalf("Source%s Go%s", out, encoded)
	}
}
