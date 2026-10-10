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

func TestSkillSchemaAndStrippingParserMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	inputs := []string{`{"skill":"commit"}`, `{"skill":" /review-pr ","args":"123"}`, `{"skill":"","future":true}`, `{"skill":"\ud800","args":"\udfff"}`, `{"skill":"a","__proto__":true,"constructor":false,"\ud800":1}`, `{}`, `null`, `true`, `[]`, `{"skill":null,"args":false}`, `{"skill":3,"args":[]}`, `{"skill":"a","args":null}`}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "parsers": inputs})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	var source struct {
		Schema  json.RawMessage
		Parsers []struct {
			Success      bool
			Data, Issues json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Parsers) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	d, e := NativeBuiltinDefinition("Skill")
	if e != nil {
		t.Fatal(e)
	}
	schemas, e := NewSession().RenderBatch([]Definition{d}, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if e != nil || len(schemas) != 1 {
		t.Fatal(e)
	}
	if !jsonSourcesEqual(raw(schemas[0]), source.Schema) {
		t.Errorf("schema Go=%s Source=%s", raw(schemas[0]), source.Schema)
	}
	for i, input := range inputs {
		parsed, e := ParseInputJSON(d, []byte(input))
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
