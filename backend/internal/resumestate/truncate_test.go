package resumestate

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
)

func resumeBoundaryRows() []map[string]any {
	user := string(plain("user", "question"))
	use := `{"type":"assistant","uuid":"tool","message":{"role":"assistant","id":"response","content":[{"type":"tool_use","id":"call","name":"Bash","input":{"command":"do not replay"}}],"stop_reason":"tool_use"}}`
	attachment := `{"type":"attachment","uuid":"attachment","attachment":{"type":"file","filename":false}}`
	final := `{"type":"assistant","uuid":"final","message":{"role":"assistant","id":"response-2","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`
	return []map[string]any{
		{"messages": []string{user, use}, "boundary": "tool"},
		{"messages": []string{user, attachment, final}, "boundary": "attachment"},
		{"messages": []string{user, final}, "boundary": "user"},
		{"messages": []string{user, final}, "boundary": "missing"},
		{"messages": []string{user, final}, "boundary": "final"},
	}
}

func TestResumeBoundaryCutsBeforeFilteringWithoutSyntheticMessages(t *testing.T) {
	for index, row := range resumeBoundaryRows() {
		home := t.TempDir()
		options, _ := testOptions(t, home, OSFiles{})
		service, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		values := row["messages"].([]string)
		raw := make([]json.RawMessage, len(values))
		for i, v := range values {
			raw[i] = json.RawMessage(v)
		}
		input := original(raw)
		input.ResumeAt = row["boundary"].(string)
		result, err := service.Load(input)
		if index == 3 {
			if err == nil || !strings.Contains(err.Error(), "No message found with message.uuid of: missing") {
				t.Fatal(result, err)
			}
			continue
		}
		if err != nil || result == nil || result.TurnInterruptionState.Kind != "none" {
			t.Fatal(result, err)
		}
		want := 1
		if index == 4 {
			want = 2
		}
		if len(result.Messages) != want {
			t.Fatal(index, "synthetic/later context survived", string(encode(result.Messages)))
		}
		for _, m := range result.Messages {
			var r map[string]any
			_ = json.Unmarshal(encode(m), &r)
			if r["uuid"] != "user" && r["uuid"] != "final" {
				t.Fatal("invented message", r)
			}
		}
		service.AwaitBackground()
	}
}

func TestResumeBoundaryMatchesActualFrozenMainLoadConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-resume-boundary.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RESUME_BOUNDARY_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	rows := resumeBoundaryRows()
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected struct{ Output []json.RawMessage }
	if json.Unmarshal(source, &expected) != nil || len(expected.Output) != len(rows) {
		t.Fatal(string(source))
	}
	for index, row := range rows {
		options, _ := testOptions(t, t.TempDir(), OSFiles{})
		service, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		values := row["messages"].([]string)
		raw := make([]json.RawMessage, len(values))
		for i, v := range values {
			raw[i] = json.RawMessage(v)
		}
		input := original(raw)
		input.ResumeAt = row["boundary"].(string)
		result, err := service.Load(input)
		var actual any
		if err != nil {
			actual = map[string]any{"error": err.Error()}
		} else {
			actual = map[string]any{"messages": result.Messages, "interruption": result.TurnInterruptionState}
		}
		var got, want any
		_ = json.Unmarshal(encode(actual), &got)
		_ = json.Unmarshal(expected.Output[index], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row %d Go%s Source%s", index, encode(actual), expected.Output[index])
		}
		service.AwaitBackground()
	}
}
