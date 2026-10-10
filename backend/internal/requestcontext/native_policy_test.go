package requestcontext

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativePolicyObservation struct {
	Policy      protocol.JSONObject `json:"policy"`
	EffortBeta  bool                `json:"effortBeta"`
	BindingBeta bool                `json:"bindingBeta"`
}

func TestNativeRequestPolicyMatchesActualSourceContract(t *testing.T) {
	raw, e := os.ReadFile("testdata/native_model_policy_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus []struct {
		Scenario    fixtureInput            `json:"scenario"`
		Observation nativePolicyObservation `json:"observation"`
		SourceOnly  struct {
			Consumer, Transport string
			Capabilities        struct{ Blocked []string }
		} `json:"sourceOnly"`
	}
	if e = json.Unmarshal(raw, &corpus); e != nil || len(corpus) != 24 {
		t.Fatalf("actual Source native corpus: %v len=%d", e, len(corpus))
	}
	for _, record := range corpus {
		t.Run(record.Scenario.Name, func(t *testing.T) {
			if record.SourceOnly.Consumer != "src/services/api/claude.ts queryModelWithStreaming" || record.SourceOnly.Transport != "guarded loopback" || len(record.SourceOnly.Capabilities.Blocked) != 0 {
				t.Fatal("Source consumer provenance/guard missing")
			}
			input, e := fixtureContext(record.Scenario, "http://127.0.0.1:43210")
			if e != nil {
				t.Fatal(e)
			}
			input.Host.Profile = NativeDirectRelayProfile
			result, e := Build(input, dependencies())
			if e != nil {
				t.Fatal(e)
			}
			var body protocol.JSONObject
			if e = body.UnmarshalJSON(result.PreparedRequest.Body); e != nil {
				t.Fatal(e)
			}
			actual := nativePolicyObservation{Policy: protocol.NewJSONObject()}
			for _, key := range []string{"model", "max_tokens", "thinking", "temperature", "output_config"} {
				if value := body.Get(key); value != nil {
					actual.Policy.Set(key, value)
				}
			}
			beta := strings.Split(result.PreparedRequest.Headers.Get("anthropic-beta"), ",")
			for _, value := range beta {
				if value == "effort-2025-11-24" {
					actual.EffortBeta = true
				}
				if value == "thinking-binding-controls-2026-08-01" {
					actual.BindingBeta = true
				}
			}
			wantRaw, _ := json.Marshal(record.Observation)
			actualRaw, _ := json.Marshal(actual)
			var want, got any
			_ = json.Unmarshal(wantRaw, &want)
			_ = json.Unmarshal(actualRaw, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("actual Source policy differs\nwant %s\ngot  %s", wantRaw, actualRaw)
			}
		})
	}
}

func TestNativeCapabilityControlsDoNotBroadenFiniteProfile(t *testing.T) {
	input := basicContext(t)
	input.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "new-provider-model"
	input.Host.Env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "thinking,effort,adaptive_thinking,max_effort"
	input.Options.Model = "new-provider-model"
	input.Thinking.Type = "adaptive"
	if _, e := Build(input, dependencies()); e == nil {
		t.Fatal("original finite profile admitted native controls")
	}
	input.Host.Profile = NativeDirectRelayProfile
	if _, e := Build(input, dependencies()); e != nil {
		t.Fatal(e)
	}
}
