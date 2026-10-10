package toolschema

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
)

func TestBackgroundTaskSchemasParsersMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]string{}
	for _, name := range []string{"TaskOutput", "TaskStop"} {
		for _, input := range []string{`{}`, `null`, `[]`, `{"task_id":"bfixture","block":"false","timeout":0}`, `{"task_id":"","block":true,"timeout":0.25}`, `{"task_id":"bfixture","block":null,"timeout":"3"}`, `{"task_id":"bfixture","timeout":-1}`, `{"task_id":"bfixture","timeout":600001}`, `{"task_id":"bfixture","shell_id":"bother"}`, `{"shell_id":"bfixture"}`, `{"task_id":null}`, `{"task_id":"\ud800","future":true}`, `{"task_id":"x","block":"TRUE"}`} {
			rows = append(rows, map[string]string{"name": name, "input": input})
		}
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "parsers": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	var source struct {
		Schemas []json.RawMessage
		Parsers []struct {
			Success      bool
			Data, Issues json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Parsers) != len(rows) || len(source.Schemas) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.2000s", e, out)
	}
	for i, name := range []string{"TaskOutput", "TaskStop"} {
		definition, e := NativeBuiltinDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		schemas, e := NewSession().RenderBatch([]Definition{definition}, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
		if e != nil || len(schemas) != 1 {
			t.Fatal(e)
		}
		if !jsonSourcesEqual(raw(schemas[0]), source.Schemas[i]) {
			t.Errorf("schema %s Go=%s Source=%s", name, raw(schemas[0]), source.Schemas[i])
		}
	}
	for i, row := range rows {
		definition, _ := NativeBuiltinDefinition(row["name"])
		got, e := ParseInputJSON(definition, []byte(row["input"]))
		want := source.Parsers[i]
		if want.Success {
			if e != nil || !jsonSourcesEqual(raw(got), want.Data) {
				t.Errorf("parser%d Go=%s %v Source=%s", i, raw(got), e, want.Data)
			}
		} else {
			var validation *ValidationError
			if !errors.As(e, &validation) || !jsonSourcesEqual(validation.Issues, want.Issues) {
				t.Errorf("parser%d Go=%v Source=%s", i, e, want.Issues)
			}
		}
	}
}
