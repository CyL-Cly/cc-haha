package jsonschemavalidator

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

func TestActualSourceSyntheticOutputGenericValidatorAndIdentity(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []struct {
		Schema json.RawMessage   `json:"schema"`
		Inputs []json.RawMessage `json:"inputs"`
	}{
		{[]byte(`{"type":"object","properties":{"count":{"type":"integer","minimum":1},"name":{"type":"string","minLength":2}},"required":["count","name"],"additionalProperties":false}`), []json.RawMessage{[]byte(`{"count":2,"name":"中文"}`), []byte(`{}`), []byte(`{"count":0.1,"name":"x","extra":true}`), []byte(`{"count":"2","name":null}`)}},
		{[]byte(`{"type":"object","properties":{"items":{"type":"array","items":{"enum":["a","b"]},"uniqueItems":true,"minItems":1,"maxItems":3}},"required":["items"]}`), []json.RawMessage{[]byte(`{"items":["a","b"]}`), []byte(`{"items":["x","x",3,true]}`), []byte(`{"items":[]}`)}},
		{[]byte(`{"type":"object","properties":{"text":{"type":"string","pattern":"^[a-z]+$","maxLength":2},"one":{"oneOf":[{"type":"integer"},{"type":"number"}]}},"required":["text"]}`), []json.RawMessage{[]byte(`{"text":"ab"}`), []byte(`{"text":"中文","one":2}`)}},
		{[]byte(`{"type":"object","$defs":{"thing":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/thing"}},"required":["x"]}`), []json.RawMessage{[]byte(`{"x":"yes"}`), []byte(`{"x":false}`)}},
		{[]byte(`{"type":"object","properties":{"answer":{"const":"𐐀"},"n":{"type":["number","null"]}},"required":["answer"]}`), []json.RawMessage{[]byte(`{"answer":"𐐀","n":null}`), []byte(`{"answer":"different","n":false}`)}},
		{[]byte(`{"type":"object","anyOf":[{"required":["a"]},{"required":["b"]}],"if":{"required":["a"]},"then":{"properties":{"a":{"type":"integer"}}},"else":{"properties":{"b":{"type":"boolean"}}}}`), []json.RawMessage{[]byte(`{"a":1}`), []byte(`{"b":false}`), []byte(`{"a":"bad"}`), []byte(`{}`)}},
		{[]byte(`{"type":"banana"}`), []json.RawMessage{}},
		{[]byte(`{"type":"object","properties":{"n":{"minimum":"bad"}}}`), []json.RawMessage{}},
		{[]byte(`{"type":"object","unknownKeyword":true}`), []json.RawMessage{}},
		{[]byte(`{"type":"object","properties":{"x":{"type":"string","format":"unknown-native-format"}}}`), []json.RawMessage{}},
		{[]byte(`{"$ref":"https://unavailable.example/schema"}`), []json.RawMessage{}},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	data, _ := json.Marshal(map[string]any{"homeDir": home, "syntheticOutputs": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("actual Source %v %.2000s %.2000s", e, stdout.String(), stderr.String())
	}
	var source struct {
		SyntheticOutputs []struct {
			Error  string
			Same   bool
			Values []struct {
				OK    bool
				Error string
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.SyntheticOutputs) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.SyntheticOutputs), source.Capabilities.Blocked)
	}
	compiler, e := New()
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		validator, e := compiler.Compile(ctx, uint64(i+1), row.Schema)
		if e != nil {
			t.Fatal(e)
		}
		expected := source.SyntheticOutputs[i]
		if validator.BuildError() != expected.Error {
			t.Fatalf("Source Ajv build%d differs Go=%q Source=%q", i, validator.BuildError(), expected.Error)
		}
		again, e := compiler.Compile(ctx, uint64(i+1), []byte(`{"different":true}`))
		if e != nil || again != validator || expected.Error == "" && !expected.Same {
			t.Fatal("real schema identity cache changed", i, e)
		}
		if expected.Error != "" {
			continue
		}
		for j, raw := range row.Inputs {
			var input protocol.JSONObject
			if e := input.UnmarshalJSON(raw); e != nil {
				t.Fatal(e)
			}
			e := validator.Validate(ctx, input)
			message := ""
			if e != nil {
				message = e.Error()
			}
			if (e == nil) != expected.Values[j].OK || message != expected.Values[j].Error {
				t.Fatalf("Source Ajv validation%d/%d differs Go=%q Source=%q", i, j, message, expected.Values[j].Error)
			}
		}
	}
	if compiler.CompiledCount() != len(rows) {
		t.Fatal("validator identity cache recompiled", compiler.CompiledCount())
	}
}
func TestGenericValidatorDoesNotExposeAmbientProcessModulesOrNetwork(t *testing.T) {
	compiler, e := New()
	if e != nil {
		t.Fatal(e)
	}
	compiler.mu.Lock()
	defer compiler.mu.Unlock()
	value, e := compiler.vm.RunScript("capabilities.js", `[typeof process,typeof require,typeof fetch,typeof Bun,typeof setTimeout,typeof __WorkflowAjv].join(',')`)
	if e != nil || value.String() != "undefined,undefined,undefined,undefined,undefined,undefined" {
		t.Fatal(e, value)
	}
}
