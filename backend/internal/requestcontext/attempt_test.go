package requestcontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestPreparedContextReusesActualSourceHistoryAndBodyAcrossAttempts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "request_context_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Scenario    fixtureInput       `json:"scenario"`
		Observation fixtureObservation `json:"observation"`
	}
	if err := json.Unmarshal(raw, &records); err != nil || len(records) != 89 {
		t.Fatalf("missing actual Source construction corpus: %v", err)
	}
	for _, record := range records {
		t.Run(record.Scenario.Name, func(t *testing.T) {
			input, err := fixtureContext(record.Scenario, "http://127.0.0.1:43210")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			prepared, err := Prepare(input, Dependencies{UUID: func() string {
				calls++
				return fmt.Sprintf("context-id-%d", calls)
			}, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				result, err := prepared.BuildAttempt(AttemptOptions{Model: input.Options.Model})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(result.PreparedRequest.Body, []byte(record.Observation.Fetches[0].Body)) {
					t.Fatalf("attempt %d changed actual Source body\nwant %s\ngot  %s", attempt, record.Observation.Fetches[0].Body, result.PreparedRequest.Body)
				}
				if calls != record.Observation.UUIDCalls {
					t.Fatalf("attempt %d allocated %d IDs, actual Source preparation allocated %d", attempt, calls, record.Observation.UUIDCalls)
				}
				// A completed attempt owns its encoded request; modifying that
				// buffer cannot contaminate the next request's construction.
				result.PreparedRequest.Body[0] = '!'
			}
		})
	}
}

func TestPreparedContextRetryBudgetKeepsOriginalModelAndSystem(t *testing.T) {
	input := basicContext(t)
	input.Options.Model = "claude-sonnet-4-20250514[1m]"
	input.Thinking = Thinking{Type: "enabled"}
	input.Host.Env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:43210/proxy"
	optionCap := 4096.0
	input.Options.MaxOutputTokensOverride = &optionCap
	prepared, err := Prepare(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	var system json.RawMessage
	for _, test := range []struct {
		name     string
		cap      *float64
		want     string
		thinking string
	}{
		{"absent retains option", nil, "4096", `{"budget_tokens":4095,"type":"enabled"}`},
		{"retry override wins", floatPointer(2048), "2048", `{"budget_tokens":2047,"type":"enabled"}`},
		{"retry disables optional thinking", floatPointer(1024), "1024", `{"type":"disabled"}`},
		{"zero retains option", floatPointer(0), "4096", `{"budget_tokens":4095,"type":"enabled"}`},
		{"NaN retains option", floatPointer(math.NaN()), "4096", `{"budget_tokens":4095,"type":"enabled"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := prepared.BuildAttempt(AttemptOptions{Model: "claude-haiku-4-5", MaxTokensOverride: test.cap})
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(result.PreparedRequest.Body, &body); err != nil {
				t.Fatal(err)
			}
			if string(body["model"]) != `"claude-sonnet-4-20250514"` || string(body["max_tokens"]) != test.want || string(body["thinking"]) != test.thinking {
				t.Fatalf("retry replaced frozen original model or budget policy: %s", result.PreparedRequest.Body)
			}
			if result.PreparedRequest.Headers.Get("anthropic-beta") != "context-1m-2025-08-07" || result.BudgetSource != "explicit" || result.PreparedRequest.Headers.Get("x-cc-haha-output-budget-source") != "explicit" {
				t.Fatal("retry lost frozen beta or output provenance", result)
			}
			// CCH signing deliberately changes the wire billing header when
			// the body budget changes. Compare the frozen original system
			// before that signing boundary, not the body-dependent signature.
			originalSystem, err := result.Params.Property("system").MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if system == nil {
				system = append(json.RawMessage(nil), originalSystem...)
			} else if !bytes.Equal(system, originalSystem) {
				t.Fatal("retry rebuilt frozen system blocks")
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestPreparedContextRejectsAbsentPreparation(t *testing.T) {
	var prepared *PreparedContext
	if result, err := prepared.BuildAttempt(AttemptOptions{}); result != nil || err == nil {
		t.Fatal("absent once-per-query preparation was accepted")
	}
	if prepared.NormalizedHistory() != nil {
		t.Fatal("absent preparation fabricated normalized history")
	}
	input := basicContext(t)
	input.Options.MaxOutputTokensOverride = floatPointer(math.NaN())
	result, err := Build(input, dependencies())
	if err != nil || result.BudgetSource != "default" {
		t.Fatal("falsey NaN invented explicit output provenance", result, err)
	}
}

func TestPreparedContextObservationCannotMutateNextRequest(t *testing.T) {
	input := basicContext(t)
	prepared, err := Prepare(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	observed := prepared.NormalizedHistory()
	if len(observed) != 1 {
		t.Fatal(observed)
	}
	observed[0].Set("message", json.RawMessage(`{"role":"user","content":"observer mutation"}`))
	result, err := prepared.BuildAttempt(AttemptOptions{})
	if err != nil || bytes.Contains(result.PreparedRequest.Body, []byte("observer mutation")) {
		t.Fatal("observation modified prepared request", result, err)
	}
}

func TestPreparedContextNativeSchemaIsResolvedOnceBeforeAttempts(t *testing.T) {
	input := basicContext(t)
	definition, err := parseObject(json.RawMessage(`{"kind":"builtin","name":"Read"}`))
	if err != nil {
		t.Fatal(err)
	}
	input.Tools = []protocol.JSONObject{definition}
	if prepared, err := Prepare(input, dependencies()); prepared != nil || err == nil {
		t.Fatal("unresolved original prompt/session host was silently approximated")
	}
	session := toolschema.NewSession()
	input.Host.ToolSchemas = session
	input.Host.ToolSchemaHost = &toolschema.Host{MainLoopModel: "fixture-model"}
	prepared, err := Prepare(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	for _, cap := range []float64{32000, 2048} {
		result, err := prepared.BuildAttempt(AttemptOptions{Model: "claude-haiku-4-5", MaxTokensOverride: &cap})
		if err != nil || !bytes.Contains(result.PreparedRequest.Body, []byte(`"name":"Read"`)) {
			t.Fatal("original native tool was lost on retry", result, err)
		}
	}
	if session.PromptCalls()["Read"] != 1 || session.CacheSize() != 1 {
		t.Fatal("native schema/prompt preparation repeated on retry", session.PromptCalls(), session.CacheSize())
	}
}
