package requestcontext

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceRequestContextGoldens(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "request_context_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Scenario    fixtureInput       `json:"scenario"`
		Observation fixtureObservation `json:"observation"`
		SourceOnly  struct {
			Runtime struct{ Platform, Arch, Version string } `json:"runtime"`
		} `json:"sourceOnly"`
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 89 {
		t.Fatal("missing actual Source request-construction corpus")
	}
	// Additional native boundary regressions are captured from actual Source
	// self replay, not inferred from the Go effort implementation.
	auxiliaryRaw, err := os.ReadFile(filepath.Join("testdata", "effort_environment_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	auxiliary := records[:0:0]
	if err := json.Unmarshal(auxiliaryRaw, &auxiliary); err != nil || len(auxiliary) != 3 {
		t.Fatalf("missing actual Source effort boundary corpus: %v", err)
	}
	records = append(records, auxiliary...)
	for _, record := range records {
		t.Run(record.Scenario.Name, func(t *testing.T) {
			input := record.Scenario
			input.Runtime.Platform = record.SourceOnly.Runtime.Platform
			input.Runtime.Arch = record.SourceOnly.Runtime.Arch
			input.Runtime.Version = record.SourceOnly.Runtime.Version
			observed, err := observeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			// Automatic net/http headers are recorded in the Go-only transport
			// observation; the Source golden freezes the complete pre-fetch seam.
			observed.wireRequests = nil
			if !reflect.DeepEqual(record.Observation, observed) {
				expected, _ := json.Marshal(record.Observation)
				actual, _ := json.Marshal(observed)
				t.Fatalf("actual Source construction differs\nexpected %s\nactual   %s", expected, actual)
			}
		})
	}
}

func basicContext(t *testing.T) Context {
	t.Helper()
	input, err := fixtureContext(fixtureInput{HistorySources: []string{`{"type":"user","uuid":"u","message":{"role":"user","content":"question"}}`}}, "http://127.0.0.1:43210")
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func dependencies() Dependencies {
	return Dependencies{UUID: func() string { return "allocated" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }}
}
func TestUnsupportedBoundariesFailBeforeTransportAndDoNotMutateHistory(t *testing.T) {
	mutations := map[string]func(*Context){
		"undeclared profile": func(input *Context) { input.Host.Profile = "" },
		"unported config environment": func(input *Context) {
			input.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL_CAPABILITIES"] = "thinking,effort"
		},
		"tools":         func(input *Context) { input.Tools = []protocol.JSONObject{protocol.NewJSONObject()} },
		"firstparty":    func(input *Context) { input.Host.Env["ANTHROPIC_BASE_URL"] = "https://api.anthropic.com" },
		"invalid base":  func(input *Context) { input.Host.Env["ANTHROPIC_BASE_URL"] = "http://[" },
		"cloud":         func(input *Context) { input.Host.Env["CLAUDE_CODE_USE_BEDROCK"] = "1" },
		"internal":      func(input *Context) { input.Host.Env["USER_TYPE"] = "ant" },
		"device":        func(input *Context) { input.Host.DeviceID = "" },
		"session":       func(input *Context) { input.Host.SessionID = "" },
		"thinking mode": func(input *Context) { input.Thinking.Type = "unknown" },
		"catalog":       func(input *Context) { input.Options.Model = "model-from-dynamic-provider-catalog" },
		"attachment":    func(input *Context) { input.History[0].Set("type", quoted("attachment")) },
		"local command": func(input *Context) {
			input.History[0].Set("type", quoted("system"))
			input.History[0].Set("subtype", quoted("local_command"))
		},
		"synthetic error": func(input *Context) { input.History[0].Set("isApiErrorMessage", json.RawMessage("true")) },
		"user malformed":  func(input *Context) { input.History[0] = replaceContent(input.History[0], json.RawMessage("4")) },
		"text malformed": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"text","text":4}]`))
		},
		"user thinking": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"thinking","thinking":"x"}]`))
		},
		"unknown block": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"image","source":{}}]`))
		},
		"nested media": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"tool_result","tool_use_id":"x","content":[{"type":"image"}]}]`))
		},
		"nested malformed": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"tool_result","tool_use_id":"x","content":4}]`))
		},
		"nested text malformed": func(input *Context) {
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"tool_result","tool_use_id":"x","content":[{"type":"text","text":4}]}]`))
		},
		"assistant tool result": func(input *Context) {
			input.History[0].Set("type", quoted("assistant"))
			input.History[0] = replaceContent(input.History[0], json.RawMessage(`[{"type":"tool_result"}]`))
		},
		"system nonstring":    func(input *Context) { input.SystemPrompt = []json.RawMessage{json.RawMessage("4")} },
		"output config array": func(input *Context) { input.Host.Env["CLAUDE_CODE_EXTRA_BODY"] = `{"output_config":[]}` },
		"malformed metadata":  func(input *Context) { input.Host.Env["CLAUDE_CODE_EXTRA_METADATA"] = `{` },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			input := basicContext(t)
			mutate(&input)
			before, _ := json.Marshal(input.History)
			result, err := Build(input, dependencies())
			var capability *CapabilityError
			if result != nil || !errors.As(err, &capability) {
				t.Fatalf("expected explicit capability rejection, got %+v %v", result, err)
			}
			if capability.Error() != "request context capability is not migrated: "+capability.Boundary {
				t.Fatal("unported input must retain explicit capability diagnostic")
			}
			after, _ := json.Marshal(input.History)
			if string(before) != string(after) {
				t.Fatal("failed construction mutated original history")
			}
		})
	}
	input := basicContext(t)
	if result, err := Build(input, Dependencies{}); result != nil || err == nil {
		t.Fatal("missing session allocator must fail")
	}
}

func TestAmbiguousImportedEnvelopeOrderFailsClosed(t *testing.T) {
	input := basicContext(t)
	assistant, _ := parseObject(json.RawMessage(`{"type":"assistant","uuid":"a","message":{"id":"a","role":"assistant","content":[{"type":"text","text":"answer"}]}}`))
	last := input.History[0].Clone()
	last.Set("message", json.RawMessage(`{"content":"next","role":"user"}`))
	input.History = append(input.History, assistant, last)
	result, err := Build(input, dependencies())
	var capability *CapabilityError
	if result != nil || !errors.As(err, &capability) || capability.Boundary != "ambiguous ordered history provenance" {
		t.Fatalf("must reject indistinguishable imported provenance: %+v %v", result, err)
	}
}

func TestProxyPolicyUsesWHATWGAndPreservesBudgetProvenance(t *testing.T) {
	for _, profile := range []struct {
		base           string
		budget, effort bool
	}{
		{"http://127.1:43210/proxy", true, true}, {"http://0x7f000001:43210/proxy/providers/p", true, true}, {"http://[::1]/proxy", true, false}, {"https://relay.invalid/proxy", false, false}, {"http://localhost/proxy/other", true, false}, {"http://localhost/proxy///", true, true}, {"http://localhost/proxy-other", false, false}, {"http://[", false, false},
	} {
		if localBudgetProxy(profile.base) != profile.budget || localEffortProxy(profile.base) != profile.effort {
			t.Fatalf("wrong URL-derived policy for %s", profile.base)
		}
	}
	input := basicContext(t)
	input.Host.Env["ANTHROPIC_BASE_URL"] = "https://relay.invalid/proxy"
	result, err := Build(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if result.PreparedRequest.Headers.Get("x-cc-haha-output-budget-source") != "" {
		t.Fatal("remote relay must not receive loopback provenance header")
	}
	input.Host.Env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = "0x20"
	result, err = Build(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if result.BudgetSource != "explicit" {
		t.Fatal("JS Number hexadecimal is explicit even though parseInt decimal falls back")
	}
}

func TestBudgetAndEffortNumericControlsDoNotInventProviderSemantics(t *testing.T) {
	input := basicContext(t)
	input.Host.Env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"] = "true"
	input.Options.EffortValue = json.RawMessage("17")
	if result, err := Build(input, dependencies()); err != nil || result.Params.Property("output_config") != nil {
		t.Fatal("external numeric effort is not an API output_config effort")
	}
}
