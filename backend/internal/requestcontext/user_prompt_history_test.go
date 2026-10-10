package requestcontext

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestActualSourceUserPromptBlockingWarningIsRetainedButNotModelContext(t *testing.T) {
	for _, content := range []string{`"UserPromptSubmit operation blocked by hook:\nactual block\n\nOriginal prompt: ORIGINAL_ERASED"`, `"UserPromptSubmit operation blocked by hook:\n\ud800\n\nOriginal prompt: [object Object],[object Object]"`} {
		scenario := fixtureInput{Name: "user-prompt-blocking-warning", HistorySources: []string{`{"type":"system","subtype":"informational","content":` + content + `,"isMeta":false,"timestamp":"2023-11-14T22:13:20.000Z","uuid":"blocking-warning","level":"warning"}`, `{"type":"user","message":{"role":"user","content":"actual next human"},"uuid":"next-user","timestamp":"2023-11-14T22:13:20.000Z"}`}}
		output := runQuestionImageSource(t, "request-context.fixture.ts", scenario, []string{"CC_HAHA_REQUEST_CONTEXT_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "ANTHROPIC_API_KEY=offline-fixture-key"})
		var source struct {
			Observation fixtureObservation
			SourceOnly  struct {
				WireRequests []fetchObservation
				Runtime      struct{ Platform, Arch, Version string }
				Capabilities struct{ Blocked []string }
			}
		}
		if err := json.Unmarshal(output, &source); err != nil || len(source.SourceOnly.WireRequests) != 1 || len(source.SourceOnly.Capabilities.Blocked) != 0 {
			t.Fatalf("actual Source normalization failed %v %.1800s", err, output)
		}
		scenario.Runtime.Platform, scenario.Runtime.Arch, scenario.Runtime.Version = source.SourceOnly.Runtime.Platform, source.SourceOnly.Runtime.Arch, source.SourceOnly.Runtime.Version
		got, err := observeFixture(scenario)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.wireRequests) != 1 || got.wireRequests[0].Body != source.SourceOnly.WireRequests[0].Body {
			t.Fatal("actual physical model payload differs from Source")
		}
		got.wireRequests = nil
		if !reflect.DeepEqual(got, source.Observation) {
			actual, _ := json.Marshal(got)
			want, _ := json.Marshal(source.Observation)
			t.Fatalf("original Prepare/normalization differs\nGo%s\nSource%s", actual, want)
		}
	}
}
