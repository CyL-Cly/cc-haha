package workflow

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
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceWorkflowLiveSchemaAdmissionSerializationAndCachedValidation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []struct {
		Body  string          `json:"body"`
		Value json.RawMessage `json:"value"`
	}{
		{`const schema={type:'object',properties:{n:{minimum:()=>1}}};try{return await agent('live schema',{schema})}catch(e){return String(e)}`, []byte(`{"n":3}`)},
		{`const schema={type:'object',unknownKeyword:()=>1};try{return await agent('live schema',{schema})}catch(e){return String(e)}`, []byte(`{"n":3}`)},
		{`const schema={get type(){throw new Error('live getter failed')}};try{return await agent('live schema',{schema})}catch(e){return String(e)}`, []byte(`{"n":3}`)},
		{`let reads=0;const schema={type:'object',properties:{n:{get minimum(){reads++;return 2}}}};const result=await agent('live schema',{schema});return {result,reads}`, []byte(`{"n":3}`)},
		{`const schema={type:'object',properties:{n:{type:'integer',minimum:2}}};Object.defineProperty(schema,'toJSON',{value:()=>({type:'object'})});try{return await agent('live schema',{schema})}catch(e){return String(e)}`, []byte(`{"n":1}`)},
		{`const schema={type:'object',properties:{n:{type:'integer',minimum:2}}};const one=await agent('first',{schema});schema.properties.n.minimum=10;const two=await agent('second',{schema});return [one,two]`, []byte(`{"n":3}`)},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "liveSchemaExecutions": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source actual live schema runtime %v %.1500s %.1500s", e, stdout.String(), stderr.String())
	}
	var source struct {
		LiveSchemaExecutions []protocol.JSONObject
		Capabilities         struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.LiveSchemaExecutions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, source.Capabilities.Blocked, stdout.String())
	}
	for i, row := range rows {
		program, e := CompileBody(utf16.Encode([]rune(row.Body)))
		if e != nil {
			t.Fatal(e)
		}
		calls := 0
		out, e := Execute(ctx, ExecutionOptions{Program: program, RunID: "wf_live123-abc", WorkflowName: "live-schema", Model: "fixture-model", Now: func() float64 { return 1700000000000 }, Emit: func(protocol.JSONObject) error { return nil }, GetTurnSpent: func() float64 { return 0 }, RunAgent: func(ctx context.Context, r AgentExecution) (AgentOutcome, error) {
			validator, raw, e := r.Schema.Prepare(ctx)
			if e != nil {
				return AgentOutcome{}, fmt.Errorf("agent({schema}): %s", e)
			}
			calls++
			if e = r.OnAgentID(fmt.Sprintf("fixture-%d", calls)); e != nil {
				return AgentOutcome{}, e
			}
			var value protocol.JSONObject
			_ = value.UnmarshalJSON(row.Value)
			if e = validator.Validate(ctx, value); e != nil {
				return AgentOutcome{}, e
			}
			return AgentOutcome{AgentID: fmt.Sprintf("fixture-%d", calls), Value: raw}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "result", Value: out.Result}, protocol.JSONField{Name: "calls", Value: valueRaw(calls)})
		if out.Error != "" {
			actual.Set("error", quoted(out.Error))
		}
		canonical := func(raw []byte) any { var v any; _ = json.Unmarshal(raw, &v); return v }
		if !reflect.DeepEqual(canonical(valueRaw(actual)), canonical(valueRaw(source.LiveSchemaExecutions[i]))) {
			t.Errorf("Source schema admission%d differs\nGo %s\nSource %s", i, valueRaw(actual), valueRaw(source.LiveSchemaExecutions[i]))
		}
	}
}
