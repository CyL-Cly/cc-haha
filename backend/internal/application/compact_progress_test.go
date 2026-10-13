package application

import (
	"encoding/json"
	"testing"
)

func TestNativeCompactProgressProjection(t *testing.T) {
	p := NewProjection()
	for _, tc := range []struct {
		input string
		chars bool
	}{{`{"type":"system","subtype":"status","status":"compacting"}`, false}, {`{"type":"system","subtype":"status","status":"compacting","compact_progress":{"output_chars":23}}`, true}} {
		frames, e := p.Translate([]byte(tc.input))
		if e != nil || len(frames) != 1 {
			t.Fatal(e, frames)
		}
		var event map[string]json.RawMessage
		_ = json.Unmarshal(frames[0], &event)
		if string(event["state"]) != `"compacting"` {
			t.Fatal(string(frames[0]))
		}
		if (event["compactProgress"] != nil) != tc.chars {
			t.Fatal(string(frames[0]))
		}
	}
}
