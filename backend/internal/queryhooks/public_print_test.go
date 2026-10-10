package queryhooks

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestPublicPrintEmptyHooksUseLiveResolver(t *testing.T) {
	for _, cfg := range []string{"", `{"hooks":{"SessionStart":[],"PreToolUse":[],"PostToolUse":[]}}`, `{"disableAllHooks":true,"hooks":{"SessionStart":[]}}`} {
		s, _, _ := newTestService(t, map[string]string{"userSettings": cfg})
		for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse"} {
			if e := s.EmptyEvent(event, "own-sid"); e != nil {
				t.Fatal(e)
			}
		}
		if e := s.Register("SessionStart", []Matcher{{Hooks: []string{"callback"}}}); e != nil {
			t.Fatal(e)
		}
		requireCapability(t, s.EmptyEvent("SessionStart", "own-sid"))
	}
}

func TestNativeSubagentEventsUseActualResolversAndKeepFiniteGuard(t *testing.T) {
	for _, event := range []string{"SubagentStart", "SubagentStop"} {
		finite, _, _ := newTestService(t, nil)
		requireCapability(t, finite.EmptyEvent(event, "actual-child-id"))
		if event == "SubagentStart" {
			requireCapability(t, finite.Register(event, []Matcher{{Hooks: []string{"callback"}}}))
		}
		native, _, _ := newTestService(t, nil)
		native.options.Profile = ResolvedSettingsProfile
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "hooks", Value: []byte(`{"SubagentStart":[],"SubagentStop":[]}`)})
		native.options.MergedSettings = func() (*protocol.JSONObject, error) { return &settings, nil }
		if e := native.EmptyEvent(event, "actual-child-id"); e != nil {
			t.Fatal(e)
		}
		if e := native.SetSession("actual-child-id", event, []Matcher{{Hooks: []string{"callback"}}}); e != nil {
			t.Fatal(e)
		}
		requireCapability(t, native.EmptyEvent(event, "actual-child-id"))
		native.ClearSession("actual-child-id")
		if e := native.Register(event, []Matcher{{Hooks: []string{"callback"}}}); e != nil {
			t.Fatal(e)
		}
		requireCapability(t, native.EmptyEvent(event, "actual-child-id"))
	}
}

func TestNativeTaskEventsUseLiveResolverAndRejectActiveHooks(t *testing.T) {
	for _, event := range []string{"TaskCreated", "TaskCompleted", "ConfigChange"} {
		finite, _, _ := newTestService(t, nil)
		requireCapability(t, finite.EmptyEvent(event, "actual-sid"))
		requireCapability(t, finite.Register(event, []Matcher{{Hooks: []string{"callback"}}}))
		native, _, _ := newTestService(t, nil)
		native.options.Profile = ResolvedSettingsProfile
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "hooks", Value: []byte(`{"TaskCreated":[],"TaskCompleted":[],"ConfigChange":[]}`)})
		native.options.MergedSettings = func() (*protocol.JSONObject, error) { return &settings, nil }
		if e := native.EmptyEvent(event, "actual-sid"); e != nil {
			t.Fatal(e)
		}
		if e := native.SetSession("actual-sid", event, []Matcher{{Hooks: []string{"callback"}}}); e != nil {
			t.Fatal(e)
		}
		requireCapability(t, native.EmptyEvent(event, "actual-sid"))
		native.ClearSession("actual-sid")
		if e := native.Register(event, []Matcher{{Hooks: []string{"function"}}}); e != nil {
			t.Fatal(e)
		}
		requireCapability(t, native.EmptyEvent(event, "actual-sid"))
	}
}
