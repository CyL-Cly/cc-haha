package toolschema_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestActualSourceToolSchemaGoldens(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "tool_schema_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Scenario    fixtureInput       `json:"scenario"`
		Observation fixtureObservation `json:"observation"`
		SourceOnly  struct {
			Runtime providerRuntime `json:"runtime"`
		} `json:"sourceOnly"`
	}
	if err = json.Unmarshal(source, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 46 {
		t.Fatal("missing actual Source original-tool corpus")
	}
	for _, record := range records {
		t.Run(record.Scenario.Name, func(t *testing.T) {
			input := record.Scenario
			input.Runtime.Platform = record.SourceOnly.Runtime.Platform
			input.Runtime.Arch = record.SourceOnly.Runtime.Arch
			input.Runtime.Version = record.SourceOnly.Runtime.Version
			actual, err := observeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, record.Observation) {
				expected, _ := json.Marshal(record.Observation)
				observed, _ := json.Marshal(actual)
				t.Fatalf("actual Source native-tool contract differs\nexpected %s\nactual   %s", expected, observed)
			}
		})
	}
}

type providerRuntime struct{ Platform, Arch, Version string }

func TestOriginalDefinitionCapabilitiesFailClosed(t *testing.T) {
	cases := map[string]string{
		"unknown native tool":           `{"kind":"builtin","name":"NotMigratedTool"}`,
		"builtin override":              `{"kind":"builtin","name":"Read","strict":true}`,
		"missing original name":         `{"kind":"registered","prompt":"x"}`,
		"definition field":              `{"kind":"registered","name":"x","prompt":"x","preparedSchema":{}}`,
		"unported callback":             `{"kind":"registered","name":"x","prompt":{}}`,
		"kind":                          `{"kind":"remote","name":"x"}`,
		"swarm":                         `{"kind":"registered","name":"Agent","prompt":"x"}`,
		"plan":                          `{"kind":"registered","name":"ExitPlanMode","prompt":"x"}`,
		"toolsearch":                    `{"kind":"registered","name":"ToolSearch","prompt":"x"}`,
		"MCP":                           `{"kind":"registered","name":"mcp__server__tool","prompt":"x"}`,
		"unpaired name":                 `{"kind":"registered","name":"\ud800","prompt":"x"}`,
		"unpaired low name":             `{"kind":"registered","name":"\udc00","prompt":"x"}`,
		"unpaired alias":                `{"kind":"registered","name":"x","aliases":["\ud800"],"prompt":"x"}`,
		"schema transforms":             `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","transform":"x"}}`,
		"schema field name":             `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","fields":[{"name":"\ud800","schema":{"kind":"string"}}]}}`,
		"bad schema policy":             `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","unknownKeys":"guess"}}`,
		"duplicate schema field":        `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","fields":[{"name":"a","schema":{"kind":"string"}},{"name":"a","schema":{"kind":"string"}}]}}`,
		"union schema":                  `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"union"}}`,
		"empty enum":                    `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"enum","values":[]}}`,
		"non-string description":        `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"string","description":4}}`,
		"original input JSON nonobject": `{"kind":"registered","name":"x","prompt":"x","inputJSONSchema":false}`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			original, err := parseObject(source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = toolschema.ParseDefinition(original)
			var capability *toolschema.CapabilityError
			if !errors.As(err, &capability) || capability.Boundary == "" {
				t.Fatalf("unported original definition must stop, got %v", err)
			}
		})
	}
}
func TestRegisteredExecutionCapabilitiesFailClosed(t *testing.T) {
	for _, original := range []string{`{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","fields":[{"name":"x","schema":{"kind":"number"}}]}}`, `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","unknownKeys":"strict"}}`, `{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","fields":[{"name":"x","schema":{"kind":"string","default":"x"}}]}}`} {
		record, _ := parseObject(original)
		definition, err := toolschema.ParseDefinition(record)
		if err != nil {
			t.Fatal(err)
		}
		_, err = toolschema.ParseInput(definition, protocol.NewJSONObject())
		var capability *toolschema.CapabilityError
		if !errors.As(err, &capability) {
			t.Fatal("unported execution parser was approximated", err)
		}
	}
	record, _ := parseObject(`{"kind":"registered","name":"x","prompt":"x"}`)
	definition, err := toolschema.ParseDefinition(record)
	if err != nil {
		t.Fatal(err)
	}
	_, err = toolschema.ParseInput(definition, protocol.JSONObject{})
	var capability *toolschema.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal("nonobject execution input must stop", err)
	}
}
func TestUnresolvedReadPromptStops(t *testing.T) {
	record, _ := parseObject(`{"kind":"builtin","name":"Read"}`)
	definition, err := toolschema.ParseDefinition(record)
	if err != nil {
		t.Fatal(err)
	}
	session := toolschema.NewSession()
	_, err = session.RenderBatch([]toolschema.Definition{definition}, toolschema.Options{})
	var capability *toolschema.CapabilityError
	if !errors.As(err, &capability) || session.CacheSize() != 0 {
		t.Fatal("unresolved original model must not cache fabricated prompt", err)
	}
}
func TestSessionIsSafeAcrossConcurrentRounds(t *testing.T) {
	record, _ := parseObject(`{"kind":"registered","name":"FixtureWrite","prompt":"original","aliases":["LegacyWrite"]}`)
	definition, err := toolschema.ParseDefinition(record)
	if err != nil {
		t.Fatal(err)
	}
	if toolschema.CanonicalName([]toolschema.Definition{definition}, "unknown") != "unknown" || toolschema.CanonicalName([]toolschema.Definition{definition}, "LegacyWrite") != "FixtureWrite" {
		t.Fatal("original alias lookup changed")
	}
	session := toolschema.NewSession()
	var workers sync.WaitGroup
	failures := make(chan error, 24)
	for index := 0; index < 24; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			schemas, err := session.RenderBatch([]toolschema.Definition{definition}, toolschema.Options{})
			if err != nil {
				failures <- err
				return
			}
			if len(schemas) != 1 || schemas[0].String("description") != "original" {
				failures <- errors.New("concurrent schema changed")
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if session.CacheSize() != 1 || session.PromptCalls()["FixtureWrite"] != 1 {
		t.Fatal("separate completed rounds must share session base cache")
	}
	session.Clear()
	if session.CacheSize() != 0 {
		t.Fatal("explicit clear retained base cache")
	}
}

func TestPublicRendererRejectsUnportedOrMalformedInputs(t *testing.T) {
	cases := []struct {
		name       string
		definition toolschema.Definition
	}{
		{"missing schema", toolschema.Definition{Kind: "registered", Name: "original", Prompt: json.RawMessage(`"prompt"`)}},
		{"invalid original JSON schema", toolschema.Definition{Kind: "registered", Name: "original", Prompt: json.RawMessage(`"prompt"`), InputJSONSchema: json.RawMessage(`{`)}},
		{"unsupported original schema", toolschema.Definition{Kind: "registered", Name: "original", Prompt: json.RawMessage(`"prompt"`), InputSchema: &toolschema.Schema{Kind: "union"}}},
		{"unported builtin prompt", toolschema.Definition{Kind: "builtin", Name: "Unported", InputSchema: &toolschema.Schema{Kind: "object"}}},
	}
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			session := toolschema.NewSession()
			_, err := session.RenderBatch([]toolschema.Definition{entry.definition}, toolschema.Options{})
			if err == nil || session.CacheSize() != 0 || len(session.PromptCalls()) != 0 {
				t.Fatal("malformed original input must fail before caching a schema", err)
			}
		})
	}
	var session toolschema.Session
	original, _ := parseObject(`{"kind":"registered","name":"Original😀","prompt":"prompt","inputSchema":{"kind":"object","fields":[]}}`)
	definition, err := toolschema.ParseDefinition(original)
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := session.RenderBatch([]toolschema.Definition{definition}, toolschema.Options{})
	if err != nil || len(schemas) != 1 || schemas[0].String("name") != "Original😀" || session.CacheSize() != 1 {
		t.Fatal("zero-value session must retain original definition", err)
	}
}
func TestNestedDeclarationsDoNotIgnoreUnsupportedTransforms(t *testing.T) {
	for _, schema := range []string{`{"kind":"object","fields":false}`, `{"kind":"object","fields":[{"name":"x","schema":4}]}`, `{"kind":"object","fields":[{"name":"x","schema":{"kind":"string"},"transform":"hidden"}]}`} {
		original, _ := parseObject(`{"kind":"registered","name":"x","prompt":"x","inputSchema":` + schema + `}`)
		_, err := toolschema.ParseDefinition(original)
		var capability *toolschema.CapabilityError
		if !errors.As(err, &capability) {
			t.Fatalf("unsupported nested declaration must fail closed: %v", err)
		}
	}
	original, _ := parseObject(`{"kind":"registered","name":"x","prompt":"x","aliases":"bad"}`)
	_, err := toolschema.ParseDefinition(original)
	var capability *toolschema.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal("malformed aliases must fail closed", err)
	}
}
func TestValidationDiagnosticContainsActualSourceIssues(t *testing.T) {
	original, _ := parseObject(`{"kind":"registered","name":"x","prompt":"x","inputSchema":{"kind":"object","fields":[{"name":"x","schema":{"kind":"string"}}]}}`)
	definition, err := toolschema.ParseDefinition(original)
	if err != nil {
		t.Fatal(err)
	}
	_, err = toolschema.ParseInput(definition, protocol.NewJSONObject())
	var invalid *toolschema.ValidationError
	if !errors.As(err, &invalid) || err.Error() != `[{"expected":"string","code":"invalid_type","path":["x"],"message":"Invalid input: expected string, received undefined"}]` {
		t.Fatal("diagnostic must retain actual Source issue message and order", err)
	}
}
