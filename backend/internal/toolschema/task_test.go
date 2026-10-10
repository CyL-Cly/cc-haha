package toolschema

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"strings"
	"testing"
)

func TestActualTaskV2SchemasPromptsAndParsersMatchSource(t *testing.T) {
	data, err := os.ReadFile("../taskstore/testdata/native_tasks_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SchemasSource string
		Observations  []struct {
			Step        struct{ Name, Tool, Operation, InputSource string }
			Observation struct {
				Success                  bool
				DataSource, IssuesSource string
			}
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 39 || !strings.Contains(corpus.SourceOnly.Consumer, "filesystem transactions") || len(corpus.SourceOnly.Capabilities.Blocked) > 0 {
		t.Fatal("missing real Source capture")
	}
	defs := []Definition{}
	for _, name := range []string{"TaskCreate", "TaskGet", "TaskUpdate", "TaskList"} {
		def, e := NativeBuiltinDefinition(name)
		if e != nil {
			t.Fatal(e)
		}
		defs = append(defs, def)
	}
	schemas, err := NewSession().RenderBatch(defs, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := protocol.StringifyValidatedJavaScriptJSON(array(schemas)), protocol.StringifyValidatedJavaScriptJSON([]byte(corpus.SchemasSource)); string(got) != string(want) {
		t.Fatalf("actual Source schemas/prompts changed:\n%s\nwant:\n%s", got, want)
	}
	for _, row := range corpus.Observations {
		if row.Step.Operation != "parse" {
			continue
		}
		t.Run(row.Step.Name, func(t *testing.T) {
			def, err := NativeBuiltinDefinition(row.Step.Tool)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseInputJSON(def, []byte(row.Step.InputSource))
			if row.Observation.Success {
				if err != nil {
					t.Fatal(err)
				}
				if !jsonSourcesEqual(raw(parsed), []byte(row.Observation.DataSource)) {
					t.Fatalf("data %s want %s", raw(parsed), row.Observation.DataSource)
				}
				return
			}
			var failure *ValidationError
			if !errors.As(err, &failure) {
				t.Fatalf("validation wrong %v", err)
			}
			if !jsonSourcesEqual(failure.Issues, []byte(row.Observation.IssuesSource)) {
				t.Fatalf("issues %s want %s", failure.Issues, row.Observation.IssuesSource)
			}
		})
	}
}
func jsonSourcesEqual(a, b json.RawMessage) bool {
	return string(protocol.StringifyValidatedJavaScriptJSON(a)) == string(protocol.StringifyValidatedJavaScriptJSON(b))
}
func TestNativeTaskNodesDoNotWidenRegisteredSchema(t *testing.T) {
	for _, kind := range []string{"record", "union", "literal"} {
		if _, err := ParseDefinition(protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote("registered")}, protocol.JSONField{Name: "name", Value: quote("test")}, protocol.JSONField{Name: "prompt", Value: quote("test")}, protocol.JSONField{Name: "inputSchema", Value: array(map[string]any{"kind": kind})})); err == nil {
			t.Fatalf("registered %s recipe widened", kind)
		}
	}
}
