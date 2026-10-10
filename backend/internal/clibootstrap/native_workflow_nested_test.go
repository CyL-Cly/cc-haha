package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNamedNestedWorkflowSingleTaskPhysicalToolsProvenanceAndResume(t *testing.T) {
	var requests atomic.Int32
	var file string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch n := requests.Add(1); n {
		case 1:
			emitPlanFixture(w, int(n), "", "BEFORE")
		case 2:
			emitNativeTaskTool(w, "nested-write", "Write", map[string]any{"file_path": file, "content": "physical-nested-workflow"})
		case 3:
			emitNativeTaskTool(w, "nested-read", "Read", map[string]any{"file_path": file})
		case 4:
			if !strings.Contains(string(body), "physical-nested-workflow") {
				t.Error("nested worker did not see actual file")
			}
			emitPlanFixture(w, int(n), "", "NESTED_VERIFIED")
		case 5:
			emitPlanFixture(w, int(n), "", "AFTER")
		case 6:
			emitNativeTaskTool(w, "nested-resume-read", "Read", map[string]any{"file_path": file})
		case 7:
			if !strings.Contains(string(body), "physical-nested-workflow") {
				t.Error("resumed nested worker lost disk")
			}
			emitPlanFixture(w, int(n), "", "NESTED_REEXECUTED")
		default:
			t.Errorf("unexpected model call %d %.500s", n, body)
			emitPlanFixture(w, int(n), "", "unexpected")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	file = filepath.Join(m.options.Native.CWD, "nested-actual.txt")
	dir := filepath.Join(m.options.Native.CWD, ".claude", "workflows")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	child := `export const meta={name:'actual-child',description:'Read native fixture',phases:[{title:'inspect'}]};log('child log');phase('inspect');return await agent('Use actual Write/Read: '+args)`
	if e := os.WriteFile(filepath.Join(dir, "child.js"), []byte(child), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	launch := func(input protocol.JSONObject) protocol.JSONObject {
		var result protocol.JSONObject
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var e error
			result, e = m.launchNativeWorkflow(ctx, scope, input, quoteMain("nested-tool"))
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return result
	}
	wait := func(id string) protocol.JSONObject {
		var result protocol.JSONObject
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			task, e := m.Controller.BackgroundTaskOnOwner(scope, id)
			if e != nil {
				return e
			}
			if task == nil {
				return fmt.Errorf("actual workflow task missing")
			}
			future, e := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, task.Join(ctx) })
			if e != nil {
				return e
			}
			if _, e = future.Await(scope); e != nil {
				return e
			}
			task, e = m.Controller.BackgroundTaskOnOwner(scope, id)
			if e != nil {
				return e
			}
			result = task.Data.Clone()
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		return result
	}
	launched := launch(protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(`export const meta={name:'nested-parent',description:'Parent and saved child'};log('parent log');const before=await agent('Before marker');const child=await workflow('actual-child','private args');const after=await agent('After marker');return {before,child,after}`)}))
	first := wait(launched.String("taskId"))
	if first.String("status") != "completed" || string(first.Get("result")) != `{"before":"BEFORE","child":"NESTED_VERIFIED","after":"AFTER"}` || requests.Load() != 5 {
		t.Fatalf("nested real Harness failed %s POST%d", rawMain(first), requests.Load())
	}
	data, e := os.ReadFile(file)
	if e != nil || string(data) != "physical-nested-workflow" {
		t.Fatal(e, string(data))
	}
	metaCount := 0
	seen := map[int]bool{}
	if e = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var object protocol.JSONObject
		if e = object.UnmarshalJSON(raw); e != nil {
			return e
		}
		wf, ok := object.Object("workflow")
		if !ok || wf.String("runId") != launched.String("runId") {
			return nil
		}
		var index int
		_ = json.Unmarshal(wf.Get("agentIndex"), &index)
		seen[index] = true
		metaCount++
		if index == 2 {
			if wf.Get("name") != nil {
				return fmt.Errorf("Source nested missing workflowName was silently replaced")
			}
		} else if wf.String("name") != "nested-parent" {
			return fmt.Errorf("parent workflow identity lost")
		}
		return nil
	}); e != nil || metaCount != 3 || !seen[1] || !seen[2] || !seen[3] {
		t.Fatal("physical shared sequence missing", e, metaCount, seen)
	}
	resumedLaunch := launch(protocol.NewJSONObject(protocol.JSONField{Name: "scriptPath", Value: launched.Get("scriptPath")}, protocol.JSONField{Name: "resumeFromRunId", Value: launched.Get("runId")}))
	resumed := wait(resumedLaunch.String("taskId"))
	if resumed.String("status") != "completed" || requests.Load() != 7 || string(resumed.Get("result")) != `{"before":"BEFORE","child":"NESTED_REEXECUTED","after":"AFTER"}` {
		t.Fatalf("Source nested no-snapshot resume contract changed %s POST%d", rawMain(resumed), requests.Load())
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		if len(tasks) != 1 || tasks[0].String("type") != "local_workflow" || tasks[0].String("workflowRunId") != launched.String("runId") {
			return fmt.Errorf("second nested or local_agent task created")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
