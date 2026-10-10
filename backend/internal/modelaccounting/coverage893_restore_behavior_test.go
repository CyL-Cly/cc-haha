package modelaccounting

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"testing"
)

func TestCoverage893RestoreRejectsBeforeChargingOrReplacingUnknownRows(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		now        func() float64
	}{
		{"negative-cost", `{"lastSessionId":"sid","lastCost":-1}`, nil},
		{"overflow-lines", `{"lastSessionId":"sid","lastLinesRemoved":2147483648}`, nil},
		{"invalid-model-list", `{"lastSessionId":"sid","lastModelUsage":[1]}`, nil},
		{"invalid-model-counter", `{"lastSessionId":"sid","lastModelUsage":{"fixture-model":{"inputTokens":"7"}}}`, nil},
		{"missing-clock", `{"lastSessionId":"sid","lastDuration":1}`, nil},
		{"invalid-clock", `{"lastSessionId":"sid","lastDuration":1}`, func() float64 { return math.NaN() }},
		{"metadata-error", `{"lastSessionId":"sid","lastModelUsage":{"private-model":{}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := cooperative.New()
			defer o.Close()
			s, e := NewOwnedSession(o, nil)
			if e != nil {
				t.Fatal(e)
			}
			reason := errors.New("actual settings getter failed")
			e = o.Run(context.Background(), func(scope *cooperative.Scope) error {
				s.cost = 11
				before, e := s.Snapshot(scope)
				if e != nil {
					return e
				}
				b, _ := before.MarshalJSON()
				resolver := RestoredModelResolver{}
				if tc.name == "metadata-error" {
					resolver.Settings = func() (*protocol.JSONObject, error) { return nil, reason }
				}
				restored, e := s.RestoreCostStateForSession(scope, "sid", storedProject(t, tc.body), resolver, tc.now)
				if e == nil || restored {
					t.Fatal("invalid persisted state admitted", restored, e)
				}
				if tc.name == "metadata-error" && e != reason {
					t.Fatal("getter error identity lost", e)
				}
				after, _ := s.Snapshot(scope)
				a, _ := after.MarshalJSON()
				if string(a) != string(b) {
					t.Fatal("failed restore changed actual ledger", string(a), string(b))
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestCoverage893RestoredResolverConfiguredLimitsAndGetterFailures(t *testing.T) {
	for _, tc := range []struct {
		name, model, config, disabled string
		want                          float64
	}{
		{"minimum", "relay:private", `{"PRIVATE":16000}`, "", 16000}, {"maximum", "private", `{"private":10000000}`, "", 10000000}, {"capped", "private", `{"private":10000000}`, " yes ", 200000}, {"invalid-json", "private", `{`, "", 200000}, {"invalid-shape", "private", `[]`, "", 200000}, {"invalid-range", "private", `{"private":15999}`, "", 200000}, {"fraction", "private", `{"private":16000.5}`, "", 200000}, {"last-normalized-key", "private", `{"PRIVATE":16000,"private:1m":32000}`, "", 32000}, {"suffix", "provider/private", `{"private":64000}`, "", 64000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := RestoredModelResolver{Environment: map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": tc.config, "CLAUDE_CODE_DISABLE_1M_CONTEXT": tc.disabled}}
			w, _, e := r.Resolve(tc.model)
			if e != nil || w != tc.want {
				t.Fatal(w, e)
			}
		})
	}
	reason := errors.New("original settings failure")
	r := RestoredModelResolver{Settings: func() (*protocol.JSONObject, error) { return nil, reason }}
	if _, _, e := r.Resolve("private-model"); e != reason {
		t.Fatal(e)
	}
	for _, model := range []string{"__proto__", "constructor"} {
		if _, _, e := (RestoredModelResolver{}).Resolve(model); e == nil {
			t.Fatal("prototype lookup silently normalized", model)
		}
	}
	r.Settings = func() (*protocol.JSONObject, error) {
		p := storedProject(t, `{"modelOverrides":{"claude-sonnet-4-6":"relay"}}`)
		return p, nil
	}
	r.Global = func() (*protocol.JSONObject, error) { return nil, reason }
	if _, _, e := r.Resolve("relay"); e != reason {
		t.Fatal("global getter failed without identity", e)
	}
	r.Global = func() (*protocol.JSONObject, error) {
		return storedProject(t, `{"clientDataCache":{"coral_reef_sonnet":"true"}}`), nil
	}
	w, out, e := r.Resolve("relay")
	if e != nil || w != 1000000 || out != 32000 {
		t.Fatal(w, out, e)
	}
	r.Settings = func() (*protocol.JSONObject, error) { return storedProject(t, `{"modelOverrides":true}`), nil }
	if _, e := r.Canonical("relay"); e == nil {
		t.Fatal("malformed override admitted")
	}
}
