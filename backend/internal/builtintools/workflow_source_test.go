package builtintools

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestActualSourceWorkflowToolSchemaPermissionMapAndPrompt(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	inputs := []json.RawMessage{[]byte(`{}`), []byte(`{"script":"return 1","args":{"unknown":true},"title":"ignored","description":"ignored","resumeFromRunId":"wf_source123-abc"}`), []byte(`{"scriptPath":"relative.js","name":"named","args":null}`), []byte(`{"args":[1,"x",false],"unknown":"strict"}`), []byte(`{"args":true,"resumeFromRunId":"invalid"}`), []byte(`{"script":false,"name":null,"resumeFromRunId":"wf_short"}`), []byte(`null`), []byte(`[]`)}
	type perm struct {
		Input          protocol.JSONObject `json:"input"`
		Context        protocol.JSONObject `json:"context"`
		NonInteractive bool                `json:"nonInteractive"`
		Ultracode      bool                `json:"ultracode"`
	}
	permissions := []perm{}
	for _, mode := range []string{"default", "bypassPermissions", "acceptEdits", "plan", "auto"} {
		for _, nonInteractive := range []bool{false, true} {
			permissions = append(permissions, perm{input(`{"name":"named"}`), input(`{"mode":"` + mode + `","alwaysDenyRules":{"userSettings":["Workflow(named)"]}}`), nonInteractive, false})
		}
	}
	permissions = append(permissions, perm{input(`{"name":"named"}`), input(`{"mode":"default","alwaysAllowRules":{"userSettings":["Workflow(named)"]}}`), false, false}, perm{input(`{"name":"named"}`), input(`{"mode":"default","alwaysAllowRules":{"session":["Workflow(named)"]},"alwaysDenyRules":{"policySettings":["Workflow(named)"]}}`), false, false}, perm{input(`{"script":"inline"}`), input(`{"mode":"default"}`), false, false}, perm{input(`{"name":"named"}`), input(`{"mode":"default","alwaysDenyRules":{"policySettings":["Workflow(named)"]}}`), false, true})
	results := []json.RawMessage{[]byte(`{"status":"async_launched","taskId":"original-task","taskType":"local_workflow","workflowName":"name","runId":"wf_source123-abc","summary":"中文","transcriptDir":"/private/fixture/transcripts","scriptPath":"/private/fixture/script.js"}`), []byte(`{"status":"async_launched","taskId":"actual","unknown":"<>&𐐀"}`)}
	s, owner, home, contextRef := fixture(t)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	data, _ := json.Marshal(map[string]any{"homeDir": home, "workflowTools": map[string]any{"inputs": inputs, "permissions": permissions, "results": results}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
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
		WorkflowTools struct {
			HasValidator                bool
			Schema                      json.RawMessage
			Prompt                      string
			Parsed, Permissions, Mapped []json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, source.Capabilities.Blocked)
	}
	canonical := func(raw json.RawMessage) any {
		var value any
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e, string(raw))
		}
		return value
	}
	compare := func(stage string, actual, expected json.RawMessage) {
		if !reflect.DeepEqual(canonical(actual), canonical(expected)) {
			t.Fatalf("Source %s changed\nGo=%s\nSource=%s", stage, actual, expected)
		}
	}
	definition, e := toolschema.NativeBuiltinDefinition("Workflow")
	if e != nil {
		t.Fatal(e)
	}
	schema, e := toolschema.OriginalInputSchema(definition)
	if e != nil {
		t.Fatal(e)
	}
	compare("schema", schema, source.WorkflowTools.Schema)
	prompt, e := toolschema.NewSession().OriginalPrompt(definition, toolschema.Host{})
	if e != nil {
		t.Fatal(e)
	}
	compare("prompt", prompt, quote(source.WorkflowTools.Prompt))
	for i, in := range inputs {
		parsed, e := toolschema.ParseInputJSON(definition, in)
		actual := protocol.NewJSONObject(field("ok", e == nil))
		if e == nil {
			actual.Set("data", raw(parsed))
		} else {
			failure, ok := e.(*toolschema.ValidationError)
			if !ok {
				t.Fatal(e)
			}
			actual.Set("issues", failure.Issues)
		}
		compare("input", raw(actual), source.WorkflowTools.Parsed[i])
	}
	s.options.Workflow = &WorkflowBindings{Enabled: func(*cooperative.Scope) (bool, error) { return true, nil }, Launch: func(context.Context, *cooperative.Scope, protocol.JSONObject, json.RawMessage) (protocol.JSONObject, error) {
		return protocol.JSONObject{}, nil
	}, Guideline: func(*cooperative.Scope) (string, error) { return "medium", nil }, AutoConsent: func(*cooperative.Scope) (bool, error) { return false, nil }, Ultracode: func(*cooperative.Scope) (bool, error) { return false, nil }}
	if e = owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, e := s.Catalog(scope)
		if e != nil {
			return e
		}
		found := false
		for _, tool := range tools {
			if tool.Name == "Workflow" {
				found = true
				if (tool.ValidateInput != nil) != source.WorkflowTools.HasValidator {
					t.Errorf("Source Workflow optional validator differs: Go%v Source%v", tool.ValidateInput != nil, source.WorkflowTools.HasValidator)
				}
			}
		}
		if !found {
			t.Error("actual Workflow registration missing")
		}
		for i, row := range permissions {
			*contextRef = row.Context.Clone()
			s.options.NonInteractive = row.NonInteractive
			s.options.Workflow.Ultracode = func(*cooperative.Scope) (bool, error) { return row.Ultracode, nil }
			decision, e := s.workflowPermission(scope, &row.Input)
			if e != nil {
				return e
			}
			actual := protocol.NewJSONObject(field("behavior", decision.Behavior))
			if decision.UpdatedInput != nil {
				actual.Set("updatedInput", raw(*decision.UpdatedInput))
			}
			actual.Set("message", decision.Message)
			actual.Set("decisionReason", decision.DecisionReason)
			actual.Set("suggestions", decision.Suggestions)
			compare("permission", raw(actual), source.WorkflowTools.Permissions[i])
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for i, result := range results {
		mapped, e := mapWorkflowResult(result, quote("actual-workflow-id"))
		if e != nil {
			t.Fatal(e)
		}
		compare("map", raw(mapped), source.WorkflowTools.Mapped[i])
	}
}
