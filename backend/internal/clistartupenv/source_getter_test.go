package clistartupenv

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestActualSourceCacheGetterKeepsSameJSONObject(t *testing.T) {
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	first, e := s.GetSettingsForSource("userSettings")
	if e != nil || first != nil {
		t.Fatal(first, e)
	}
	original := protocol.NewJSONObject(protocol.JSONField{Name: "disableWorkflows", Value: []byte("true")})
	s.options.SettingsState.SetCachedSettingsForSource("userSettings", &original)
	a, e := s.GetSettingsForSource("userSettings")
	if e != nil || a != &original {
		t.Fatal("cache identity", a, e)
	}
	original.Set("added", []byte("true"))
	b, e := s.GetSettingsForSource("userSettings")
	if e != nil || b != a || string(b.Get("added")) != "true" {
		t.Fatal("live cached fields", b, e)
	}
	if _, e = s.GetSettingsForSource("not-a-source"); e == nil {
		t.Fatal("unknown source accepted")
	}
	_ = s.options.Environment.SetProperty(quote("USER_TYPE"), quote("ant"))
	if _, e = s.GetSettingsForSource("policySettings"); e == nil {
		t.Fatal("changed host controls accepted")
	}
}
