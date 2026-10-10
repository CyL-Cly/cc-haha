package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeMCPOrdinaryWorkflowResolutionErrorsAndCallerFileActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	script := `export const meta={name:'caller-owned',description:'actual caller script'};log('actual caller');return args`
	path := filepath.Join(home, "owned-workflow.js")
	_ = os.WriteFile(path, []byte(script), 0600)
	inputs := []map[string]any{{"scriptPath": "missing.js"}, {"name": "fixture-missing"}, {}, {"scriptPath": "owned-workflow.js", "script": "ignored invalid", "name": "fixture-missing", "args": map[string]any{"actual": true}}}
	source := startOrdinarySource(t, home, home, env)
	want := []any{}
	for _, input := range inputs {
		want = append(want, mcpOrdinaryResult(t, source.call(t, "Workflow", input)))
	}
	source.close(t)
	host, _ := ordinaryGoContext(t, home, home, env)
	for i, input := range inputs {
		got, e := host.Call(context.Background(), "Workflow", rawMain(input))
		if e != nil {
			t.Fatal(e)
		}
		a := mcpOrdinaryResult(t, got)
		if i == 3 {
			stripMCPWorkflowIdentity(a)
			stripMCPWorkflowIdentity(want[i])
		}
		if !reflect.DeepEqual(a, want[i]) {
			t.Errorf("actual Workflow resolution %d Go=%v Source=%v", i, a, want[i])
		}
	}
	if e := host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(path); e != nil || string(b) != script {
		t.Fatal("caller script was overwritten", e, string(b))
	}
}
func stripMCPWorkflowIdentity(root any) {
	value, ok := root.(map[string]any)
	if !ok {
		return
	}
	blocks, _ := value["content"].([]any)
	for _, block := range blocks {
		b, _ := block.(map[string]any)
		if data, ok := b["text"].(map[string]any); ok {
			for _, key := range []string{"taskId", "runId", "transcriptDir"} {
				delete(data, key)
			}
		}
	}
}
