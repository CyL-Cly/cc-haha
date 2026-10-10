package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

// Both runtimes execute the real orchestration and physical journal. Only the
// child Harness boundary is replaced here; actual model/child IO is tested by
// the MainPrint integration, rather than called a model E2E in this test.
func TestActualSourceWorkflowProgressJournalReplayAndBudget(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{
		{"body": `phase('inspect');return await agent('  inspect\nreal file  ',{label:'  named\n worker ',phase:'',model:'other-model',effort:'low',agentType:'Explore',unknown:'ignored'})`, "phases": []string{"first"}, "journal": true},
		{"body": `await agent('one');return await agent('two')`, "journal": true},
		{"body": `await agent('one');return await agent('two')`, "journal": true, "seed": []map[string]any{{"prompt": "one", "agentId": "old-one", "result": "cached one"}, {"prompt": "two", "agentId": "old-two", "result": "cached two"}}},
		{"body": `await agent('changed');return await agent('two')`, "journal": true, "seed": []map[string]any{{"prompt": "one", "agentId": "old-one", "result": "cached one"}, {"prompt": "two", "agentId": "old-two", "result": "cached two"}}},
		{"body": `await agent('one');await agent('changed');return await agent('three')`, "journal": true, "seed": []map[string]any{{"prompt": "one", "agentId": "old-one", "result": "cached one"}, {"prompt": "two", "agentId": "old-two", "result": "cached two"}, {"prompt": "three", "agentId": "old-three", "result": "cached three"}}},
		{"body": `await agent('one');return await agent('two')`, "journal": true, "seed": []map[string]any{{"prompt": "one", "agentId": "stopped-agent", "started": true}, {"prompt": "two", "agentId": "old-two", "result": "stale"}}},
		{"body": `return await agent('failed')`, "journal": true, "agentError": "actual lower Harness failure"},
		{"body": `return await agent('budget exhausted')`, "budget": 12500.0, "spent": 12750.125},
		{"body": `await agent('one');return await agent('two')`, "budget": 3.0, "spendPerAgent": 3.0},
		{"body": `return [await parallel([]),await pipeline([],null)]`, "budget": 1.0, "spent": 1.0},
		{"body": `return await parallel([null])`, "budget": 1.0, "spent": 1.0},
		{"body": `return await pipeline([1],null)`, "budget": 1.0, "spent": 1.0},
		{"body": `return await parallel([()=>{throw {name:'WorkflowBudgetExceededError',message:'one'}},()=>1,()=>{throw {name:'WorkflowBudgetExceededError',message:'two'}}])`},
		{"body": `return await pipeline([1,2],()=>{throw {name:'WorkflowBudgetExceededError',message:'drop'}})`},
		{"body": `return await agent('remote',{isolation:'remote'})`},
	}
	child := `export const meta={name:'child',description:'Actual child',phases:[{title:'inspect'}]};log('child-log');phase('inspect');return await agent('child-'+args)`
	rows = append(rows,
		map[string]any{"body": `log('parent-before');await agent('before');const child=await workflow('child','arg');log('parent-after');await agent('after');return child`, "nested": map[string]string{"child": child}, "journal": true},
		map[string]any{"body": `return await workflow('child')`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return await workflow('child')`}},
		map[string]any{"body": `return await workflow('missing')`, "nested": map[string]string{"child": child}},
		map[string]any{"body": `return await workflow(3)`, "nested": map[string]string{"child": child}},
		map[string]any{"body": `return await workflow('child',{data:[1,'中文']})`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return args`}},
	)
	rows = append(rows, map[string]any{"body": `const value={label:'before'};const pending=workflow('child',value);value.label='after';return await pending`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return args`}})
	rows = append(rows, map[string]any{"body": `const value={};value.self=value;return await workflow('child',value)`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return args`}}, map[string]any{"body": `return await workflow('child',NaN)`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return Number.isNaN(args)`}}, map[string]any{"body": `return await workflow('child',Infinity)`, "nested": map[string]string{"child": `export const meta={name:'child',description:'Actual child'};return args===Infinity`}})
	for i, row := range rows {
		row["detailed"] = true
		row["script"] = fmt.Sprintf("export const meta={name:'replay-%d',description:'Original progress and journal'};\n%s", i, row["body"])
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "executions": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source execution %v %.2000s %.2000s", e, stdout.String(), stderr.String())
	}
	var source struct {
		Executions   []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Executions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Executions), source.Capabilities.Blocked)
	}
	canonical := func(raw []byte) any {
		var value any
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	for i, row := range rows {
		program, e := CompileBody(utf16.Encode([]rune(row["body"].(string))))
		if e != nil {
			t.Fatal(e)
		}
		events := []protocol.JSONObject{}
		calls := []protocol.JSONObject{}
		controllers := []protocol.JSONObject{}
		var mu sync.Mutex
		spent, _ := row["spent"].(float64)
		opts := ExecutionOptions{Program: program, RunID: "wf_source123", WorkflowName: fmt.Sprintf("replay-%d", i), Model: "fixture-model", Now: func() float64 { return 1700000000000 }, Emit: func(event protocol.JSONObject) error { events = append(events, event.Clone()); return nil }, GetTurnSpent: func() float64 { mu.Lock(); defer mu.Unlock(); return spent }}
		if nested, ok := row["nested"].(map[string]string); ok {
			parserOwner := cooperative.New()
			defer parserOwner.Close()
			opts.PrepareNested = func(ctx context.Context, ref json.RawMessage) (PreparedScript, error) {
				units, e := protocol.DecodeJavaScriptJSONString(ref)
				if e != nil {
					return PreparedScript{}, e
				}
				name := string(utf16.Decode(units))
				script, ok := nested[name]
				if !ok {
					return PreparedScript{}, fmt.Errorf("workflow(): unknown workflow '%s'", name)
				}
				var result PreparedScript
				e = parserOwner.Run(ctx, func(scope *cooperative.Scope) error {
					metadata, e := workflowcommands.NewMetadata(scope, parserOwner)
					if e != nil {
						return e
					}
					parsed, e := metadata.Parse(scope, utf16.Encode([]rune(script)))
					if e != nil {
						return e
					}
					if parsed.ErrorUnits != nil {
						return errors.New("workflow(): " + parsed.Error)
					}
					result.Meta = parsed.Meta.Clone()
					result.Program, e = CompileBody(parsed.ScriptBodyUnits)
					return e
				})
				return result, e
			}
		}
		if total, ok := row["budget"].(float64); ok {
			opts.BudgetTotal = &total
		}
		if phases, ok := row["phases"].([]string); ok {
			opts.SeedPhaseTitles = phases
		}
		if row["journal"] == true {
			opts.Journal, e = NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"), NativeJournalFiles())
			if e != nil {
				t.Fatal(e)
			}
			previous := quoted("")
			if seeds, ok := row["seed"].([]map[string]any); ok {
				for _, seed := range seeds {
					previous, e = CacheKey(quoted(seed["prompt"].(string)), []byte(`{}`), previous)
					if e != nil {
						t.Fatal(e)
					}
					typ := "result"
					if seed["started"] == true {
						typ = "started"
					}
					entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted(typ)}, protocol.JSONField{Name: "key", Value: previous}, protocol.JSONField{Name: "agentId", Value: valueRaw(seed["agentId"])})
					if typ == "result" {
						entry.Set("result", valueRaw(seed["result"]))
					}
					pending, e := opts.Journal.BeginAppend(entry)
					if e != nil {
						t.Fatal(e)
					}
					<-pending.Done()
					if e = pending.Err(); e != nil {
						t.Fatal(e)
					}
				}
			}
			snapshot, e := opts.Journal.Load()
			if e != nil {
				t.Fatal(e)
			}
			opts.JournalSnapshot = &snapshot
		}
		opts.OnAgentController = func(key string, cancel context.CancelCauseFunc) error {
			controllers = append(controllers, protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: quoted(key)}, protocol.JSONField{Name: "active", Value: valueRaw(cancel != nil)}))
			return nil
		}
		opts.RunAgent = func(ctx context.Context, request AgentExecution) (AgentOutcome, error) {
			provenance := protocol.NewJSONObject(protocol.JSONField{Name: "runId", Value: quoted(request.RunID)}, protocol.JSONField{Name: "name", Value: quoted(request.WorkflowName)}, protocol.JSONField{Name: "phaseIndex", Value: valueRaw(request.PhaseIndex)}, protocol.JSONField{Name: "agentIndex", Value: valueRaw(request.Index)})
			if request.WorkflowName == "" {
				provenance.Delete("name")
			}
			if request.PhaseTitle != "" {
				provenance.Set("phaseTitle", quoted(request.PhaseTitle))
			}
			calls = append(calls, protocol.NewJSONObject(protocol.JSONField{Name: "prompt", Value: quoted(request.Prompt)}, protocol.JSONField{Name: "options", Value: valueRaw(request.Options)}, protocol.JSONField{Name: "workflow", Value: valueRaw(provenance)}))
			id := fmt.Sprintf("fixture-%d", request.Index)
			if e := request.OnAgentID(id); e != nil {
				return AgentOutcome{}, e
			}
			if e := request.OnProgress(AgentProgress{Tokens: 3, ToolCalls: 1, LastToolName: "Read"}); e != nil {
				return AgentOutcome{}, e
			}
			if delta, ok := row["spendPerAgent"].(float64); ok {
				mu.Lock()
				spent += delta
				mu.Unlock()
			}
			if failure, ok := row["agentError"].(string); ok {
				return AgentOutcome{}, errors.New(failure)
			}
			return AgentOutcome{AgentID: id, Value: quoted(request.Prompt), Tokens: 3, ToolCalls: 1}, nil
		}
		outcome, e := Execute(ctx, opts)
		if e != nil {
			t.Fatal(e)
		}
		journal := ""
		if opts.Journal != nil {
			data, e := os.ReadFile(opts.Journal.Path())
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				t.Fatal(e)
			}
			journal = string(data)
		}
		out := map[string]any{"outcome": map[string]any{"result": json.RawMessage(outcome.Result), "agentCount": outcome.AgentCount, "logs": outcome.Logs, "failures": outcome.Failures, "durationMs": outcome.DurationMillis}, "events": events, "calls": calls, "controllers": controllers, "journal": journal}
		if outcome.Error != "" {
			out["outcome"].(map[string]any)["error"] = outcome.Error
		}
		data, _ := json.Marshal(out)
		if !reflect.DeepEqual(canonical(data), canonical(source.Executions[i])) {
			t.Fatalf("Source journal/progress%d differs\nGo=%s\nSource=%s", i, data, source.Executions[i])
		}
	}
}
