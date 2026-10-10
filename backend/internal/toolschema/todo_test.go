package toolschema

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeTodoSchemaPromptAndParserMatchActualSource(t *testing.T) {
	data, err := os.ReadFile("testdata/native_todo_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SchemaSource string
		Observations []struct {
			Step        struct{ Name, Operation, InputSource string }
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
	if len(corpus.Observations) != 20 || !strings.Contains(corpus.SourceOnly.Consumer, "TodoWriteTool.ts") || len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("missing actual Source capture provenance")
	}
	definition, err := NativeBuiltinDefinition("TodoWrite")
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := NewSession().RenderBatch([]Definition{definition}, Options{Host: Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if err != nil || len(schemas) != 1 {
		t.Fatal(err)
	}
	if got, want := protocol.StringifyValidatedJavaScriptJSON(raw(schemas[0])), protocol.StringifyValidatedJavaScriptJSON([]byte(corpus.SchemaSource)); string(got) != string(want) {
		t.Fatalf("actual TodoWrite API schema/prompt changed:\n%s\nwant:\n%s", got, want)
	}
	for _, row := range corpus.Observations {
		if row.Step.Operation != "parse" {
			continue
		}
		t.Run(row.Step.Name, func(t *testing.T) {
			parsed, err := ParseInputJSON(definition, []byte(row.Step.InputSource))
			if row.Observation.Success {
				if err != nil {
					t.Fatal(err)
				}
				if got, want := protocol.StringifyValidatedJavaScriptJSON(raw(parsed)), protocol.StringifyValidatedJavaScriptJSON([]byte(row.Observation.DataSource)); string(got) != string(want) {
					t.Fatalf("Source parsed input changed: %s != %s", got, want)
				}
				return
			}
			var failure *ValidationError
			if !errors.As(err, &failure) {
				t.Fatalf("Source invalid input accepted or wrong error: %v", err)
			}
			if got, want := protocol.StringifyValidatedJavaScriptJSON(failure.Issues), protocol.StringifyValidatedJavaScriptJSON([]byte(row.Observation.IssuesSource)); string(got) != string(want) {
				t.Fatalf("Source validation issues changed: %s != %s", got, want)
			}
		})
	}
}

func TestNativeTodoRecipeDoesNotWidenFiniteRegisteredSchema(t *testing.T) {
	var definition protocol.JSONObject
	if err := json.Unmarshal([]byte(`{"kind":"registered","name":"array-tool","prompt":"prompt","inputSchema":{"kind":"object","fields":[{"name":"todos","schema":{"kind":"array"}}]}}`), &definition); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDefinition(definition); err == nil {
		t.Fatal("native array recipe widened finite registered schema")
	}
}
