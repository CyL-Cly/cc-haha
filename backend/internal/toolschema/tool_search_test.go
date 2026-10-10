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

func TestToolSearchSchemaAndParserMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	inputs := []string{`{"query":"Read"}`, `{"query":"","future":true}`, `{"query":"\ud800","max_results":-1}`, `{"query":"Read","max_results":0}`, `{"query":"Read","max_results":2.9}`, `{"query":"Read","max_results":1e25}`, `{"query":"Read","max_results":"5"}`, `{"query":"Read","max_results":null}`, `{"query":"Read","max_results":[]}`, `{"query":null}`, `{"query":true}`, `{}`, `null`, `[]`, `false`, `{"query":"Read","__proto__":true,"constructor":false,"\ud800":1}`}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-search.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_SEARCH_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "parsers": inputs})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Schema  json.RawMessage
		Parsers []struct {
			Success      bool
			Data, Issues json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Parsers) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %d %v", e, len(source.Parsers), source.Capabilities.Blocked)
	}
	definition, e := NativeBuiltinDefinition("ToolSearch")
	if e != nil {
		t.Fatal(e)
	}
	schemas, e := NewSession().RenderBatch([]Definition{definition}, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if e != nil || len(schemas) != 1 {
		t.Fatal(e)
	}
	if !jsonSourcesEqual(raw(schemas[0]), source.Schema) {
		t.Errorf("schema Go=%s Source=%s", raw(schemas[0]), source.Schema)
	}
	for i, input := range inputs {
		parsed, e := ParseInputJSON(definition, []byte(input))
		expected := source.Parsers[i]
		if expected.Success {
			if e != nil || !jsonSourcesEqual(raw(parsed), expected.Data) {
				t.Errorf("parser%d Go=%s/%v Source=%s", i, raw(parsed), e, expected.Data)
			}
			continue
		}
		var invalid *ValidationError
		if !errors.As(e, &invalid) || !jsonSourcesEqual(invalid.Issues, expected.Issues) {
			t.Errorf("parser%d Go=%v Source=%s", i, e, expected.Issues)
		}
	}
}
