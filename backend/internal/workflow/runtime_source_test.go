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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func TestActualSourceWorkflowLanguagePlainAndSequentialAgentRuntime(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	bodies := []string{`console.log('from console',{n:1});console.info(null,undefined,()=>1);console.warn(1n,Symbol('a'));console.error({get value(){throw Error('bad')}});console.debug();return Object.isFrozen(console)&&Object.getPrototypeOf(console)===null`, `return {ordered:true,x:[1,2],f:()=>3}`, `return args.nested[1]`, `log({message:'中文'});phase('one');log('finish');return null`, `const first=await agent('actual one');const second=await agent(first+' actual two');return second`, `return Date.now()`, `return new Date()`, `return Math.random()`, `const now=Date.now;return now()`, `return new Date('2020-01-01').toISOString()`, `return eval('1')`, `return ({}).constructor.constructor('return process')()`, `return ()=>1`, `return await parallel([()=>agent('one'),()=>agent('two'),()=>3])`, `return await parallel([()=>1,()=>{throw new Error('actual failure')},()=>2])`, `return await parallel([1])`, `return await parallel('invalid')`, `return await pipeline([1,2],x=>x+1,x=>x*2)`, `return await pipeline([1,null,2],x=>x===2?null:x,x=>x+1)`, `return await pipeline([],null)`, `return await pipeline([1],null)`, `return await pipeline([1,2],x=>{throw new Error('stage failure')})`, `await new Promise(resolve=>setTimeout(resolve,1));return 7`, `setTimeout(()=>log('must never fire'),10);return 1`, `await new Promise(resolve=>{const h=setTimeout(()=>log('must never fire'),10);clearTimeout(h);setTimeout(resolve,1)});return 3`, `setTimeout(null,0);return 1`, `throw {name:'Custom',message:'exact original error'}`}
	rows := []map[string]any{}
	scripts := []string{}
	for i, body := range bodies {
		script := fmt.Sprintf("export const meta={name:'case-%d',description:'Actual original runtime'};\n%s", i, body)
		scripts = append(scripts, script)
		rows = append(rows, map[string]any{"script": script, "args": map[string]any{"nested": []any{1, "actual"}}})
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "executions": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source runtime %v %.1700s %.1700s", e, stdout.String(), stderr.String())
	}
	var source struct {
		Executions []struct {
			Result         json.RawMessage
			AgentCount     int
			Logs, Failures []string
			DurationMs     float64
			Error          string
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Executions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Executions), source.Capabilities.Blocked, stderr.String())
	}
	owner := cooperative.New()
	defer owner.Close()
	canonical := func(raw []byte) any { var value any; _ = json.Unmarshal(raw, &value); return value }
	for i, script := range scripts {
		var body []uint16
		if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
			metadata, e := workflowcommands.NewMetadata(scope, owner)
			if e != nil {
				return e
			}
			parsed, e := metadata.Parse(scope, utf16.Encode([]rune(script)))
			body = parsed.ScriptBodyUnits
			return e
		}); e != nil {
			t.Fatal(e)
		}
		program, e := CompileBody(body)
		if e != nil {
			t.Fatal(e)
		}
		outcome, e := Execute(ctx, ExecutionOptions{Program: program, RunID: "wf_source123", WorkflowName: fmt.Sprintf("case-%d", i), Model: "fixture-model", Args: []byte(`{"nested":[1,"actual"]}`), Now: func() float64 { return 1700000000000 }, GetTurnSpent: func() float64 { return 0 }, RunAgent: func(_ context.Context, request AgentExecution) (AgentOutcome, error) {
			return AgentOutcome{AgentID: fmt.Sprintf("fixture-%d", request.Index), Value: quoted(request.Prompt)}, nil
		}, Emit: func(protocol.JSONObject) error { return nil }})
		if e != nil {
			t.Fatal(e)
		}
		expected := source.Executions[i]
		if !reflect.DeepEqual(canonical(outcome.Result), canonical(expected.Result)) || outcome.AgentCount != expected.AgentCount || outcome.Error != expected.Error || !reflect.DeepEqual(outcome.Logs, expected.Logs) || !reflect.DeepEqual(outcome.Failures, expected.Failures) || outcome.DurationMillis != expected.DurationMs {
			t.Fatalf("Source language%d differs: Go=%+v\nSource=%+v", i, outcome, expected)
		}
	}
}
