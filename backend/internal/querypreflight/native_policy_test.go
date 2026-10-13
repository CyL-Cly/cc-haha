package querypreflight

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativePreflightBudgetAndSignaturesMatchActualSource(t *testing.T) {
	data, e := os.ReadFile("testdata/native_preflight_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Observations []struct {
			Scenario struct {
				Name    string
				Model   json.RawMessage
				Env     map[string]string
				History []protocol.JSONObject
			}
			Observation struct {
				Effective, Threshold float64
				History              []protocol.JSONObject
				SameInputArray       bool
			}
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if e = json.Unmarshal(data, &corpus); e != nil {
		t.Fatal(e)
	}
	if len(corpus.Observations) != 23 || len(corpus.SourceOnly.Capabilities.Blocked) != 0 || !strings.Contains(corpus.SourceOnly.Consumer, "autoCompact.ts") {
		t.Fatal("missing actual Source capture provenance")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			var model string
			if e := json.Unmarshal(row.Scenario.Model, &model); e != nil {
				t.Fatal(e)
			}
			effective, threshold := nativeThresholds(model, row.Scenario.Env)
			if effective != row.Observation.Effective || threshold != row.Observation.Threshold {
				t.Fatalf("actual Source budget mismatch: got %v/%v, want %v/%v", effective, threshold, row.Observation.Effective, row.Observation.Threshold)
			}
			projected, e := preflightcore.StripProtectedSignatures(row.Scenario.History, preflightcore.ModelInput{Profile: preflightcore.NativeDirectRelayProfile, Original: row.Scenario.Model, Environment: modelEnvironment(row.Scenario.Env)})
			if e != nil {
				t.Fatal(e)
			}
			got := make([]protocol.JSONObject, 0, len(projected.Records))
			for _, item := range projected.Records {
				got = append(got, item.Record)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(row.Observation.History)
			if string(protocol.StringifyValidatedJavaScriptJSON(gotJSON)) != string(protocol.StringifyValidatedJavaScriptJSON(wantJSON)) || projected.SameInputArray != row.Observation.SameInputArray {
				t.Fatalf("Source signature projection mismatch: %s != %s (same=%v)", gotJSON, wantJSON, projected.SameInputArray)
			}
		})
	}
}

func TestNativePreflightActuallyBlocksConfiguredWindowWhileKeepingFiniteBoundary(t *testing.T) {
	s, _, _, _, _ := testServices(t)
	in := base(t)
	in.Profile = NativeProcessProfile
	in.MainLoopModel = "actual-custom-small"
	in.PermissionMode = "acceptEdits"
	in.OriginalEnvironment = map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"actual-custom-small":16000}`, "DISABLE_AUTO_COMPACT": "1"}
	in.History = []queryloop.Message{{Record: record(t, `{"type":"assistant","message":{"role":"assistant","model":"actual-custom-small","content":[{"type":"text","text":"actual response"}],"usage":{"input_tokens":9000,"output_tokens":1}}}`)}}
	got, e := s.Run(context.Background(), in)
	if e != nil || got.Blocked == nil || got.Statistics.BlockingEstimate < 9000 {
		t.Fatalf("native configured window was ignored: %+v %v", got, e)
	}
	in.Profile = ""
	if _, e = s.Run(context.Background(), in); e == nil {
		t.Fatal("old finite model boundary broadened")
	}
	in.Profile = NativeProcessProfile
	in.OriginalEnvironment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"actual-custom-small":64000}`
	got, e = s.Run(context.Background(), in)
	if e != nil || got.Blocked != nil {
		t.Fatalf("actual updated budget did not affect next preflight: %+v %v", got, e)
	}
}

func TestNativeBudgetControlsNeverMutateEnvironmentOrSilentlyResolveMainAlias(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"custom:1M":32000}`, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "2000"}
	before := map[string]string{}
	for k, v := range env {
		before[k] = v
	}
	nativeThresholds("provider/custom:1m", env)
	if !reflect.DeepEqual(env, before) {
		t.Fatal("original env was mutated")
	}
	for _, model := range []string{"opus", "sonnet[1m]", "best"} {
		if _, e := nativeModel(model); e == nil {
			t.Fatalf("unresolved native startup alias %s", model)
		}
	}
}
