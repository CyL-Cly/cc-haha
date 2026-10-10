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

func TestMCPResourceToolSchemasAndParsersMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]string{}
	for _, name := range []string{"ListMcpResourcesTool", "ReadMcpResourceTool"} {
		for _, input := range []string{`{}`, `{"server":"fixture","uri":"scheme:path"}`, `{"server":"","uri":"","future":true}`, `{"server":"\ud800","uri":"\udfff"}`, `{"server":null,"uri":false}`, `null`, `{"server":3,"uri":[]}`} {
			rows = append(rows, map[string]string{"name": name, "input": input})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "resourceParsers": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2400s", e, output)
	}
	var source struct {
		Schemas         []json.RawMessage
		ResourceParsers []struct {
			Success      bool
			Data, Issues json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.ResourceParsers) != len(rows) || len(source.Schemas) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.ResourceParsers), source.Capabilities.Blocked)
	}
	for index, name := range []string{"ListMcpResourcesTool", "ReadMcpResourceTool"} {
		definition, e := NativeBuiltinDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		schema, e := NewSession().RenderBatch([]Definition{definition}, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
		if e != nil || len(schema) != 1 {
			t.Fatal(e)
		}
		if !jsonSourcesEqual(raw(schema[0]), source.Schemas[index]) {
			t.Errorf("schema %s Go=%s Source=%s", name, raw(schema[0]), source.Schemas[index])
		}
	}
	for i, row := range rows {
		definition, _ := NativeBuiltinDefinition(row["name"])
		parsed, e := ParseInputJSON(definition, []byte(row["input"]))
		expected := source.ResourceParsers[i]
		if expected.Success {
			if e != nil || !jsonSourcesEqual(raw(parsed), expected.Data) {
				t.Errorf("parser%d Go=%s/%v Source=%s", i, raw(parsed), e, expected.Data)
			}
		} else {
			var invalid *ValidationError
			if !errors.As(e, &invalid) || !jsonSourcesEqual(invalid.Issues, expected.Issues) {
				t.Errorf("parser%d Go=%v Source=%s", i, e, expected.Issues)
			}
		}
	}
}
