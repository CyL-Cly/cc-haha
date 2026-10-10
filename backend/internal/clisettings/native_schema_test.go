package clisettings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSettingsReadsRulesWhileFiniteProfileRetainsBoundary(t *testing.T) {
	content := `{"permissions":{"allow":["Read","Bash(git status:*)"],"deny":["Edit(secret/**)"],"ask":["Bash(rm *)"],"future":{"preserve":true}},"futureRoot":{"keep":3}}`
	parsed, e := ValidateNativeJSONContent(content, "fixture")
	if e != nil || parsed.Settings == nil {
		t.Fatalf("native rules %v %+v", e, parsed)
	}
	if !strings.Contains(string(parsed.Settings.Get("permissions")), `"allow":["Read","Bash(git status:*)"]`) || string(parsed.Settings.Get("futureRoot")) != `{"keep":3}` {
		t.Fatal(parsed.Settings)
	}
	if _, e = ValidateJSONContent(content, "finite"); e == nil {
		t.Fatal("original finite schema was broadened")
	}
	s, state, _ := testService(t, nil, nil)
	s.options.Profile = NativeProcessProfile
	path := filepath.Join(s.options.ConfigHome, "settings.json")
	if e = os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	parsed, e = s.ParseFile(path)
	if e != nil || parsed.Settings == nil {
		t.Fatal(e)
	}
	state.SetCachedSettingsForSource("userSettings", parsed.Settings)
	got, e := s.GetEditableSource("userSettings", "")
	if e != nil || got != parsed.Settings {
		t.Fatalf("native source/cache identity %v", e)
	}
	_, e = s.UpdateEditableSource("userSettings", "", Patch{Object: object(t, `{"permissions":{"allow":["Grep"]}}`)})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(b), "futureRoot") || !strings.Contains(string(b), "Grep") {
		t.Fatalf("native additive write %s %v", b, e)
	}
}

func TestNativeSettingsInvalidRuleConsumersStopExplicitly(t *testing.T) {
	for _, input := range []string{`{"permissions":{"allow":["Read(\ud800)"]}}`, `{"permissions":{"deny":["Skill\ud800(name)"]}}`, `{"permissions":{"ask":["Skill(\ud800) trailing"]}}`, `{"permissions":{"allow":["skill(\ud800)"]}}`, `{"permissions":{"allow":["read"]}}`, `{"permissions":{"allow":["Bash()"]}}`, `{"permissions":{"deny":["Bash(x:* trailing)"]}}`, `{"permissions":{"ask":[null]}}`, `{"permissions":{"allow":false}}`, `{"permissions":{"allow":["mcp____tool"]}}`} {
		if _, e := ValidateNativeJSONContent(input, "fixture"); e == nil {
			t.Fatalf("unported invalid-rule warning/filter silently admitted %s", input)
		}
	}
}

func TestNativeSettingsExplicitlyDisabledTeamsPreserveSettingAndFiniteGuard(t *testing.T) {
	content := `{"agentTeamsEnabled":false,"future":{"preserve":true},"effortLevel":"low"}`
	parsed, e := ValidateNativeJSONContent(content, "fixture")
	if e != nil || parsed.Settings == nil || string(parsed.Settings.Get("agentTeamsEnabled")) != "false" || string(parsed.Settings.Get("future")) != `{"preserve":true}` {
		t.Fatalf("disabled native teams %v %+v", e, parsed)
	}
	if _, e = ValidateJSONContent(content, "finite"); e == nil {
		t.Fatal("finite profile agentTeams boundary was broadened")
	}
	parsed, e = ValidateNativeJSONContent(`{"agentTeamsEnabled":true,"future":{"preserve":true}}`, "fixture")
	if e != nil || parsed.Settings == nil || string(parsed.Settings.Get("agentTeamsEnabled")) != "true" {
		t.Fatal("native original boolean parser", e, parsed)
	}
	for _, value := range []string{"null", `"false"`} {
		parsed, e = ValidateNativeJSONContent(`{"agentTeamsEnabled":`+value+`}`, "fixture")
		if e != nil || parsed.Settings != nil || len(parsed.Errors) != 1 {
			t.Fatalf("invalid optional boolean %s %v %+v", value, e, parsed)
		}
	}
}

func TestNativeSettingsThinkingBooleanPreservedWithFiniteGuard(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		content := `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":` + value + `,"effortLevel":"low","future":7}`
		parsed, e := ValidateNativeJSONContent(content, "fixture")
		if e != nil || parsed.Settings == nil || string(parsed.Settings.Get("alwaysThinkingEnabled")) != value || string(parsed.Settings.Get("agentTeamsEnabled")) != "false" || string(parsed.Settings.Get("future")) != "7" {
			t.Fatalf("native thinking boolean %s: %v %+v", value, e, parsed)
		}
		if _, e = ValidateJSONContent(content, "finite"); e == nil {
			t.Fatal("finite thinking field guard broadened")
		}
	}
	for _, value := range []string{"null", `"false"`, "0"} {
		if _, e := ValidateNativeJSONContent(`{"alwaysThinkingEnabled":`+value+`}`, "fixture"); e == nil {
			t.Fatalf("invalid native thinking setting %s", value)
		}
	}
}

func TestNativeLanguagePreservesRawValueWhileFiniteGuardRemains(t *testing.T) {
	for _, value := range []string{`"中文"`, `""`, `"future\ud800"`} {
		parsed, e := ValidateNativeJSONContent(`{"language":`+value+`,"alwaysThinkingEnabled":false,"future":7}`, "fixture")
		if e != nil || parsed.Settings == nil || string(parsed.Settings.Get("language")) != value || string(parsed.Settings.Get("future")) != "7" {
			t.Fatalf("native language %s: %v %+v", value, e, parsed)
		}
		if _, e = ValidateJSONContent(`{"language":`+value+`}`, "finite"); e == nil {
			t.Fatal("finite language guard broadened")
		}
	}
	for _, value := range []string{"false", "null", "{}"} {
		if _, e := ValidateNativeJSONContent(`{"language":`+value+`}`, "fixture"); e == nil {
			t.Fatal("invalid language", value)
		}
	}
}
