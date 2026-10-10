package jsonschemavalidator

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func TestActualSourceLiveSchemaTypesGettersIdentityAndValidation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []struct {
		Factory string            `json:"factory"`
		Inputs  []json.RawMessage `json:"inputs"`
	}{
		{`(()=>({schema:{type:'object',properties:{n:{minimum:()=>1}}}}))()`, nil},
		{`(()=>({schema:{type:'object',properties:{n:{minimum:undefined}}}}))()`, []json.RawMessage{[]byte(`{"n":0}`)}},
		{`(()=>({schema:{type:'object',properties:{n:{minimum:NaN}}}}))()`, nil},
		{`(()=>({schema:{type:'object',unknownKeyword:()=>1}}))()`, nil},
		{`(()=>{let count=0;return{schema:{type:'object',properties:{n:{get minimum(){count++;return 2}}}},reads:()=>count}})()`, []json.RawMessage{[]byte(`{"n":1}`), []byte(`{"n":3}`)}},
		{`(()=>({schema:{get type(){throw new Error('live schema getter failed')}}}))()`, nil},
		{`(()=>{const schema={type:'object',properties:{n:{type:'integer',minimum:2}}};Object.defineProperty(schema,'toJSON',{value:()=>({type:'object'})});return{schema}})()`, []json.RawMessage{[]byte(`{"n":1}`), []byte(`{"n":3}`)}},
		{`(()=>{const schema={type:'object',properties:{n:{type:'integer',minimum:2}}};return{schema,mutate:()=>{schema.properties.n.minimum=10}}})()`, []json.RawMessage{[]byte(`{"n":1}`), []byte(`{"n":3}`)}},
		{`(()=>({schema:Object.freeze({type:'object',properties:Object.freeze({n:Object.freeze({type:'integer',minimum:2})}),required:Object.freeze(['n'])})}))()`, []json.RawMessage{[]byte(`{"n":1}`), []byte(`{"n":3}`), []byte(`{}`)}},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "liveSyntheticOutputs": rows})
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
		t.Fatalf("actual Source schema %v %.1200s %.1200s", e, stdout.String(), stderr.String())
	}
	var source struct {
		LiveSyntheticOutputs []protocol.JSONObject
		Capabilities         struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.LiveSyntheticOutputs) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, source.Capabilities.Blocked, stdout.String())
	}
	for i, row := range rows {
		vm := goja.New()
		factory, e := NewActorFactory(vm)
		if e != nil {
			t.Fatal(e)
		}
		if e = factory.SealOnActor(); e != nil {
			t.Fatal(e)
		}
		value, e := vm.RunScript("caller-schema.js", row.Factory)
		if e != nil {
			t.Fatal(e)
		}
		owned := value.ToObject(vm)
		compiler, e := factory.CompilerOnActor()
		if e != nil {
			t.Fatal(e)
		}
		schema := owned.Get("schema").ToObject(vm)
		validator, e := compiler.CompileOnActor(schema)
		if e != nil {
			t.Fatal(e)
		}
		values := []protocol.JSONObject{}
		if validator.BuildError() == "" {
			for _, raw := range row.Inputs {
				e := validator.ValidateJSONOnActor(raw)
				v := protocol.NewJSONObject(protocol.JSONField{Name: "ok", Value: []byte("true")})
				if e != nil {
					v.Set("ok", []byte("false"))
					v.Set("error", mustActorJSON(e.Error()))
				}
				values = append(values, v)
			}
		}
		if mutate, ok := goja.AssertFunction(owned.Get("mutate")); ok {
			if _, e := mutate(goja.Undefined()); e != nil {
				t.Fatal(e)
			}
		}
		same, e := compiler.CompileOnActor(schema)
		if e != nil {
			t.Fatal(e)
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "values", Value: mustActorJSON(values)}, protocol.JSONField{Name: "reads", Value: []byte("0")}, protocol.JSONField{Name: "same", Value: mustActorJSON(same == validator)})
		if validator.BuildError() != "" {
			actual.Set("error", mustActorJSON(validator.BuildError()))
		}
		if read, ok := goja.AssertFunction(owned.Get("reads")); ok {
			v, e := read(goja.Undefined())
			if e != nil {
				t.Fatal(e)
			}
			actual.Set("reads", mustActorJSON(v.Export()))
		}
		canonical := func(raw []byte) any { var v any; _ = json.Unmarshal(raw, &v); return v }
		if !reflect.DeepEqual(canonical(mustActorJSON(actual)), canonical(mustActorJSON(source.LiveSyntheticOutputs[i]))) {
			t.Fatalf("Source live schema %d differs\nGo %s\nSource %s", i, mustActorJSON(actual), mustActorJSON(source.LiveSyntheticOutputs[i]))
		}
	}
}
func mustActorJSON(v any) json.RawMessage {
	raw, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return raw
}
func TestActorCompilerKeepsCodeGenerationPrivateAndRejectsModifiedIntrinsics(t *testing.T) {
	vm := goja.New()
	factory, e := NewActorFactory(vm)
	if e != nil {
		t.Fatal(e)
	}
	_, e = vm.RunScript("no-user-codegen.js", `(()=>{const blocked=()=>{throw new EvalError('Code generation from strings disallowed for this context')};Object.defineProperty(Function.prototype,'constructor',{value:blocked});globalThis.Function=blocked;globalThis.eval=blocked})()`)
	if e != nil {
		t.Fatal(e)
	}
	if e = factory.SealOnActor(); e != nil {
		t.Fatal(e)
	}
	compiler, e := factory.CompilerOnActor()
	if e != nil {
		t.Fatal(e)
	}
	schema, e := vm.RunScript("actual-schema.js", `({type:'object',properties:{n:{type:'integer'}},required:['n']})`)
	if e != nil {
		t.Fatal(e)
	}
	v, e := compiler.CompileOnActor(schema.ToObject(vm))
	if e != nil || v.BuildError() != "" {
		t.Fatal(e, v)
	}
	if e = v.ValidateJSONOnActor([]byte(`{"n":1}`)); e != nil {
		t.Fatal(e)
	}
	escaped, e := vm.RunScript("capabilities.js", `[typeof process,typeof require,typeof fetch,typeof Bun,typeof __WorkflowAjv,(()=>{try{({}).constructor.constructor('return 1')();return 'escaped'}catch(e){return e.message}})()].join(',')`)
	if e != nil || escaped.String() != "undefined,undefined,undefined,undefined,undefined,Code generation from strings disallowed for this context" {
		t.Fatal(e, escaped)
	}
	_, e = vm.RunScript("mutate-intrinsic.js", `JSON.stringify=()=> 'changed'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = compiler.CompileOnActor(schema.ToObject(vm)); e == nil {
		t.Fatal("modified host-realm intrinsic silently accepted")
	}
}
