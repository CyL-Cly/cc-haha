package queryloop

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func testEnvelope(inputs ...string) *streamhistory.Envelope {
	blocks := []*streamhistory.Block{{Object: fixtureObject(`{"type":"text","text":"plain"}`)}}
	for _, input := range inputs {
		blocks = append(blocks, &streamhistory.Block{Object: fixtureObject(`{"type":"tool_use","id":"tool","name":"write","input":` + input + `}`)})
	}
	return &streamhistory.Envelope{Record: fixtureObject(`{"type":"assistant","uuid":"original"}`), Message: fixtureObject(`{"id":"model","role":"assistant","content":[],"usage":{}}`), Content: blocks, Usage: &streamhistory.Usage{Raw: []byte(`{"input_tokens":2,"output_tokens":0}`)}, UUID: "original"}
}

func TestObservableBackfillClonesOnlyAddedFieldsAndRetainsUsageAlias(t *testing.T) {
	original := testEnvelope(`{"file_path":"relative","\ud800":1,"\udfff":2}`, `{"preserved":true}`)
	var calls []string
	clone, err := observableEnvelope(original, func(name string) (Backfill, error) {
		if name != "write" {
			t.Fatal("unexpected lookup")
		}
		return func(input *protocol.JSONObject) error {
			calls = append(calls, fixtureBytes(*input))
			if input.Get("file_path") != nil {
				input.Set("legacy", quoted("derived"))
				input.Set("file_path", quoted("expanded"))
			}
			return nil
		}, nil
	})
	if err != nil || clone == original || clone.Content[0] != original.Content[0] || clone.Content[1] == original.Content[1] || clone.Content[2] != original.Content[2] || clone.Usage != original.Usage || len(calls) != 2 {
		t.Fatalf("shallow identity/lookup changed: %v %v", calls, err)
	}
	before, err := original.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "legacy") || strings.Contains(string(before), "expanded") || !strings.Contains(string(before), `"\ud800":1,"\udfff":2`) {
		t.Fatalf("original model input changed: %s", before)
	}
	clone.Usage.Raw = []byte(`{"input_tokens":2,"output_tokens":7}`)
	after, err := clone.MarshalJSON()
	if err != nil || !strings.Contains(string(after), `"output_tokens":7`) || !strings.Contains(string(after), `"legacy":"derived"`) {
		t.Fatalf("live observable usage/update=%s %v", after, err)
	}
	model, _ := original.MarshalJSON()
	if !strings.Contains(string(model), `"output_tokens":7`) {
		t.Fatalf("usage alias was frozen: %s", model)
	}
}

func TestObservableOverwriteAndPrototypeFieldsDoNotClone(t *testing.T) {
	for _, name := range []string{"file_path", "constructor", "toString", "__proto__"} {
		t.Run(name, func(t *testing.T) {
			original := testEnvelope(`{"file_path":"relative"}`)
			output, err := observableEnvelope(original, func(string) (Backfill, error) {
				return func(input *protocol.JSONObject) error { input.Set(name, quoted("changed")); return nil }, nil
			})
			if err != nil || output != original || strings.Contains(fixtureBytes(output.Content[1].Object), "changed") {
				t.Fatalf("overwrite/inherited field triggered observable clone: %v", err)
			}
		})
	}
	for _, raw := range []string{`null`, `false`, `4`, `"string"`} {
		original := testEnvelope(raw)
		output, err := observableEnvelope(original, func(string) (Backfill, error) { t.Fatal("primitive input invoked backfill"); return nil, nil })
		if err != nil || output != original {
			t.Fatalf("primitive changed: %s %v", raw, err)
		}
	}
	original := testEnvelope(`[]`)
	output, err := observableEnvelope(original, func(string) (Backfill, error) { return nil, nil })
	if err != nil || output != original {
		t.Fatalf("unregistered array backfill changed: %v", err)
	}
	_, err = observableEnvelope(original, func(string) (Backfill, error) { return func(*protocol.JSONObject) error { return nil }, nil })
	scope(t, err, "array input observable spread")
}

func TestObservableMutationErrorsDoNotCommitModelHistory(t *testing.T) {
	original := testEnvelope(`{"known":1}`)
	before, _ := original.MarshalJSON()
	for _, backfill := range []Backfill{
		func(input *protocol.JSONObject) error { input.Set("added", []byte(`{`)); return nil },
		func(input *protocol.JSONObject) error { *input = protocol.JSONObject{}; return nil },
	} {
		if _, err := observableEnvelope(original, func(string) (Backfill, error) { return backfill, nil }); err == nil {
			t.Fatal("invalid external backfill committed")
		}
		after, _ := original.MarshalJSON()
		if string(before) != string(after) {
			t.Fatal("failed backfill mutated internal history")
		}
	}
	broken := fixtureObject(`{"before":1}`)
	broken.Set("before", json.RawMessage(`{`))
	if _, err := addedOwnField(broken, fixtureObject(`{}`)); err == nil {
		t.Fatal("invalid original escaped validation")
	}
	original.Content[1].Object.Set("input", []byte(`{`))
	if _, err := observableEnvelope(original, func(string) (Backfill, error) { return func(*protocol.JSONObject) error { return nil }, nil }); err == nil {
		t.Fatal("malformed mutable input accepted")
	}
	sentinel := errors.New("tool registry unavailable")
	if _, err := observableEnvelope(testEnvelope(`{}`), func(string) (Backfill, error) { return nil, sentinel }); err != sentinel {
		t.Fatal("lookup error changed")
	}
}
