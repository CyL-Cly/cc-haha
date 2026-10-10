package requestcontext

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestCoverage893NextRequestFiltersSyntheticErrorsAndPreservesOriginalHistory(t *testing.T) {
	for _, tc := range []struct {
		name, content, code string
		refused             bool
	}{
		{"ordinary-error", `[{"type":"text","text":"private stale API error"}]`, "", false},
		{"empty-error", `[]`, "", false},
		{"thinking-error", `[{"type":"thinking","thinking":"private stale API error"}]`, "", false},
		{"null-text", `[{"type":"text","text":null}]`, "", false},
		{"non-string-text", `[{"type":"text","text":42}]`, "", true},
		{"prototype-text", `[{"type":"text","text":"__proto__"}]`, "", true},
		{"prototype-code", `[{"type":"text","text":"private stale API error"}]`, "constructor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := basicContext(t)
			source := `{"type":"assistant","uuid":"synthetic-error","isApiErrorMessage":true,"future":{"kept":"😀"},"message":{"role":"assistant","model":"<synthetic>","content":` + tc.content + `}}`
			var row protocol.JSONObject
			if e := row.UnmarshalJSON([]byte(source)); e != nil {
				t.Fatal(e)
			}
			if tc.code != "" {
				row.Set("businessErrorCode", quoted(tc.code))
			}
			input.History = append(input.History, row)
			before, _ := json.Marshal(input.History)
			prepared, e := Prepare(input, dependencies())
			if tc.refused {
				if e == nil || prepared != nil {
					t.Fatal("unported anchor lookup was dropped silently", e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				history := prepared.NormalizedHistory()
				if len(history) != 1 || history[0].String("uuid") != "u" {
					t.Fatal("original next request retained synthetic error", history)
				}
				r, e := prepared.BuildAttempt(AttemptOptions{Model: input.Options.Model})
				if e != nil {
					t.Fatal(e)
				}
				if bytes.Contains(r.PreparedRequest.Body, []byte("private stale API error")) {
					t.Fatal("synthetic display error leaked to provider")
				}
			}
			after, _ := json.Marshal(input.History)
			if !bytes.Equal(before, after) {
				t.Fatal("next request changed actual mutable history")
			}
		})
	}
}
func TestCoverage893MalformedSyntheticPayloadDoesNotClaimSupportedRecovery(t *testing.T) {
	var row protocol.JSONObject
	if e := row.UnmarshalJSON([]byte(`{"type":"assistant","isApiErrorMessage":true,"message":[]}`)); e != nil {
		t.Fatal(e)
	}
	if IsSyntheticAPIErrorMessage(row) {
		t.Fatal("malformed API payload classified as valid synthetic error")
	}
	input := basicContext(t)
	input.History = append(input.History, row)
	if p, e := Prepare(input, dependencies()); e == nil || p != nil {
		t.Fatal("malformed marked payload silently normalized", e)
	}
}
