package uipreferences

import (
	"encoding/json"
	"testing"
)

func TestVoiceReadUpgradeDefaultsOnPreservesExplicitOff(t *testing.T) {
	for _, raw := range []string{`{"schemaVersion":3,"future":true}`, `{"schemaVersion":3,"voiceInput":{"future":"keep"}}`, `{"voiceInput":{"enabled":false,"future":"keep"}}`} {
		got, valid, e := normalize(json.RawMessage(raw))
		if e != nil || !valid {
			t.Fatal(e)
		}
		value, _ := parseObject(json.RawMessage(got))
		voice, _ := parseObject(value.get("voiceInput"))
		want := "true"
		if raw == `{"voiceInput":{"enabled":false,"future":"keep"}}` {
			want = "false"
		}
		if string(voice.get("enabled")) != want {
			t.Fatal(string(got))
		}
		if raw != `{"schemaVersion":3,"future":true}` && string(voice.get("future")) != `"keep"` {
			t.Fatal("unknown field lost", string(got))
		}
	}
}
