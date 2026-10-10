package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInvisibleAttachmentsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	rows := []originalCase{}
	actual := []observation{}
	for _, kind := range []string{"already_read_file", "command_permissions", "edited_image_file", "hook_cancelled", "hook_error_during_execution", "hook_non_blocking_error", "hook_system_message", "structured_output", "hook_permission_decision", "dynamic_skill"} {
		payload := `{"type":"` + kind + `","content":"protected bookkeeping must not become API text","model":"owned-model","allowedTools":["Read"],"future":{"keep":true}}`
		for _, mode := range []string{"projection", "history"} {
			source := payload
			if mode == "history" {
				source = `[{"type":"user","message":{"role":"user","content":"before"},"uuid":"original-id","timestamp":"original"},{"type":"attachment","attachment":` + payload + `,"uuid":"bookkeeping","timestamp":"original"}]`
			}
			row := originalCase{Name: kind + "-" + mode, Kind: mode, Source: source}
			rows = append(rows, row)
			actual = append(actual, runCase(row))
		}
	}
	for i, payload := range []string{`{"type":"hook_success","hookEvent":"SessionStart","hookName":"initial","content":"physical context\ud800"}`, `{"type":"hook_success","hookEvent":"UserPromptSubmit","hookName":false,"content":false}`, `{"type":"hook_success","hookEvent":"SessionStart","content":""}`, `{"type":"hook_additional_context","hookName":"PreToolUse:Write","content":["one","two\ud800",null,3,false,["nested",null]]}`, `{"type":"hook_additional_context","content":[]}`, `{"type":"hook_additional_context","content":null}`, `{"type":"hook_additional_context"}`, `{"type":"hook_additional_context","content":false}`, `{"type":"hook_additional_context","content":{"length":0}}`, `{"type":"hook_additional_context","content":[{"toString":false}]}`, `{"type":"hook_success","hookEvent":"Stop","content":"source hidden"}`, `{"type":"hook_success","hookEvent":"SubagentStop","content":"source hidden"}`, `{"type":"hook_success","content":"source hidden"}`, `{"type":"hook_stopped_continuation","hookName":"Stop","message":"private\ud800"}`, `{"type":"hook_stopped_continuation","hookName":false,"message":{"future":true}}`, `{"type":"hook_blocking_error","hookName":"SubagentStop","blockingError":{"command":"cmd\ud800","blockingError":"why\udfff"}}`, `{"type":"hook_blocking_error","hookName":"Stop","blockingError":{"command":false,"blockingError":["a","b"]}}`} {
		for _, mode := range []string{"projection", "history"} {
			source := payload
			if mode == "history" {
				source = `[{"type":"user","message":{"role":"user","content":"before"},"uuid":"original-id","timestamp":"original"},{"type":"attachment","attachment":` + payload + `,"uuid":"hook-context","timestamp":"original"}]`
			}
			row := originalCase{Name: fmt.Sprintf("command-hook-%d-%s", i, mode), Kind: mode, Source: source}
			rows = append(rows, row)
			actual = append(actual, runCase(row))
		}
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/retained-skills.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "CC_HAHA_RETAINED_SKILLS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	inputs := []map[string]string{}
	for _, row := range rows {
		inputs = append(inputs, map[string]string{"name": row.Name, "kind": row.Kind, "source": row.Source})
	}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": inputs})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source invisible attachments: %v %s", err, stderr.String())
	}
	var result struct{ Observations []observation }
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, result.Observations) {
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(result.Observations)
		t.Fatalf("Go=%s Source=%s", a, b)
	}
}
