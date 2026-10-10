package requestcontext

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMaxTurnsRecoveryNoResponseRetainedWhileAPIErrorFiltered(t *testing.T) {
	for _, flag := range []string{"false", "absent", "true"} {
		t.Run(flag, func(t *testing.T) {
			input := basicContext(t)
			var row protocol.JSONObject
			// JSON source is literal original producer structure, not a Source observation.
			_ = row.UnmarshalJSON([]byte(`{"type":"assistant","uuid":"recovery-id","future":{"units":"\ud800<&>"},"message":{"id":"recovery-message","role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}}`))
			if flag != "absent" {
				row.Set("isApiErrorMessage", json.RawMessage(flag))
			}
			input.History = append(input.History, row, object(field("type", quoted("user")), field("message", json.RawMessage(`{"role":"user","content":"actual recovery follow-up"}`))))
			before, _ := json.Marshal(input.History)
			prepared, err := Prepare(input, dependencies())
			if err != nil {
				t.Fatal(err)
			}
			actual, err := prepared.BuildAttempt(AttemptOptions{Model: input.Options.Model})
			if err != nil {
				t.Fatal(err)
			}
			present := bytes.Contains(actual.PreparedRequest.Body, []byte("No response requested."))
			if present != (flag != "true") {
				t.Fatalf("original flag %s changed API filtering: %s", flag, actual.PreparedRequest.Body)
			}
			after, _ := json.Marshal(input.History)
			if !bytes.Equal(before, after) {
				t.Fatal("API filtering mutated live recovery history")
			}
		})
	}
}
func TestMaxTurnsRecoveryAdmissionDoesNotHideUnportedErrorFlagsOrContent(t *testing.T) {
	base := object(field("type", quoted("assistant")), field("isApiErrorMessage", json.RawMessage("false")), field("message", json.RawMessage(`{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}`)))
	for _, mutate := range []func(*protocol.JSONObject){
		func(r *protocol.JSONObject) { r.Set("type", quoted("user")) },
		func(r *protocol.JSONObject) { r.Set("isApiErrorMessage", json.RawMessage("null")) },
		func(r *protocol.JSONObject) { r.Set("isApiErrorMessage", json.RawMessage("true")) },
		func(r *protocol.JSONObject) { r.Set("isApiErrorMessage", json.RawMessage(`"false"`)) },
		func(r *protocol.JSONObject) { r.Set("isVirtual", json.RawMessage("false")) },
		func(r *protocol.JSONObject) { r.Set("apiError", quoted("rate_limit")) },
		func(r *protocol.JSONObject) { r.Set("message", json.RawMessage(`[]`)) },
		func(r *protocol.JSONObject) {
			r.Set("message", json.RawMessage(`{"model":"provider","content":[{"type":"text","text":"No response requested."}]}`))
		},
		func(r *protocol.JSONObject) {
			r.Set("message", json.RawMessage(`{"model":"<synthetic>","content":[{"type":"thinking","thinking":"No response requested."}]}`))
		},
		func(r *protocol.JSONObject) {
			r.Set("message", json.RawMessage(`{"model":"<synthetic>","content":"No response requested."}`))
		},
	} {
		r := base.Clone()
		mutate(&r)
		if IsOriginalNoResponseAssistant(r) {
			t.Fatalf("non-factory recovery admitted: %s", stringMustJSON(r))
		}
	}
}

func stringMustJSON(r protocol.JSONObject) string { b, _ := r.MarshalJSON(); return string(b) }
