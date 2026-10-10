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

func TestActualPlanToolSchemasPromptsAndPassthroughParsers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(config, 0700)
	inputs := []struct{ Tool, Input string }{
		{"EnterPlanMode", `{}`}, {"EnterPlanMode", `{"extra":true}`}, {"EnterPlanMode", `{"__proto__":1,"\ud800":1}`},
		{"ExitPlanMode", `{}`}, {"ExitPlanMode", `{"plan":"owned","planFilePath":"owned","future":{"keep":true}}`}, {"ExitPlanMode", `{"allowedPrompts":[{"tool":"Bash","prompt":"run tests","extra":1}],"future":true}`}, {"ExitPlanMode", `{"allowedPrompts":[{"tool":"B\u0061sh","prompt":"\ud800"}]}`}, {"ExitPlanMode", `{"__proto__":1,"constructor":2,"toString":3,"\ud800":4}`},
		{"ExitPlanMode", `null`}, {"ExitPlanMode", `{"allowedPrompts":null}`}, {"ExitPlanMode", `{"allowedPrompts":[null,{}, {"tool":"Read","prompt":null}]}`}, {"ExitPlanMode", `{"allowedPrompts":[{"tool":1,"prompt":false}]}`},
	}
	rows := []map[string]string{}
	for _, input := range inputs {
		rows = append(rows, map[string]string{"tool": input.Tool, "input": input.Input})
	}
	apiInputs := []string{`{"plan":"disk","planFilePath":"/p","allowedPrompts":[{"tool":"Bash","prompt":"tests"}],"future":{"keep":1}}`, `{"0":"first","plan":null,"future":"\ud800","planFilePath":{},"1":"second"}`, `{"plan":false,"planFilePath":2,"constructor":true}`, `{"future":1}`, `null`, `false`, `"literal"`, `[1,{"plan":"inside-array"}]`}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "inputs": rows, "apiInputs": apiInputs})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(data)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source: %v %.4000s", e, output)
	}
	var source struct {
		Schemas      []string
		Observations []struct {
			Success      bool
			Data, Issues string
		}
		Capabilities struct{ Blocked []string }
		APIInputs    []json.RawMessage
	}
	if e := json.Unmarshal(output, &source); e != nil {
		t.Fatalf("Source JSON: %v %.4000s", e, output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal(source.Capabilities.Blocked)
	}
	if len(source.APIInputs) != len(apiInputs) {
		t.Fatal("missing Source API normalization cases")
	}
	for i, input := range apiInputs {
		actual := NormalizeAPIInput("ExitPlanMode", json.RawMessage(input))
		if !jsonSourcesEqual(actual, source.APIInputs[i]) {
			t.Errorf("API case%d Source=%s Go=%s", i, source.APIInputs[i], actual)
		}
	}
	defs := []Definition{}
	for _, name := range []string{"EnterPlanMode", "ExitPlanMode"} {
		d, e := NativeBuiltinDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		defs = append(defs, d)
	}
	for i, profile := range []struct {
		userType  string
		interview bool
	}{{"external", false}, {"external", true}, {"ant", true}, {"ant", true}} {
		schemas, e := NewSession().RenderBatch(defs, Options{Host: Host{BaseURL: "http://127.0.0.1:1", PlanInterviewPhase: profile.interview, PlanUserType: profile.userType}, Model: "deepseek-flash"})
		if e != nil {
			t.Fatal(e)
		}
		if !jsonSourcesEqual(array(schemas), []byte(source.Schemas[i])) {
			t.Errorf("Source schema%d=%s\nGo=%s", i, source.Schemas[i], array(schemas))
		}
	}
	if len(source.Observations) != len(inputs) {
		t.Fatal("missing Source cases")
	}
	for i, input := range inputs {
		d, _ := NativeBuiltinDefinition(input.Tool)
		parsed, e := ParseInputJSON(d, []byte(input.Input))
		expected := source.Observations[i]
		if expected.Success {
			if e != nil || !jsonSourcesEqual(raw(parsed), []byte(expected.Data)) {
				t.Errorf("case%d Source=%s Go=%s %v", i, expected.Data, raw(parsed), e)
			}
		} else {
			var invalid *ValidationError
			if !errors.As(e, &invalid) {
				t.Errorf("case%d expected Source rejection %v", i, e)
			} else if !jsonSourcesEqual(invalid.Issues, []byte(expected.Issues)) {
				t.Errorf("case%d Source=%s Go=%s", i, expected.Issues, invalid.Issues)
			}
		}
	}
}
