package clisettings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusLineSettingsParseAndWriteForDesktopAndCLIProfiles(t *testing.T) {
	content := `{"statusLine":{"type":"command","command":"printf ready","padding":2,"future":"drop"},"futureRoot":{"preserve":true}}`
	for _, profile := range []string{ExternalReleaseProfile, NativeProcessProfile} {
		t.Run(profile, func(t *testing.T) {
			var parsed *ParsedSettings
			var err error
			if profile == NativeProcessProfile {
				parsed, err = ValidateNativeJSONContent(content, "fixture")
			} else {
				parsed, err = ValidateJSONContent(content, "fixture")
			}
			if err != nil || parsed.Settings == nil {
				t.Fatalf("valid statusLine was rejected: %v %+v", err, parsed)
			}
			if got := string(parsed.Settings.Get("statusLine")); got != `{"type":"command","command":"printf ready","padding":2}` {
				t.Fatalf("statusLine shape = %s", got)
			}
			if got := string(parsed.Settings.Get("futureRoot")); got != `{"preserve":true}` {
				t.Fatalf("unknown top-level setting not preserved: %s", got)
			}
		})
	}

	for _, invalid := range []string{`{"statusLine":null}`, `{"statusLine":{"type":"agent","command":"printf ready"}}`, `{"statusLine":{"type":"command"}}`, `{"statusLine":{"type":"command","command":"printf ready","padding":"2"}}`} {
		parsed, err := ValidateJSONContent(invalid, "fixture")
		if err != nil || parsed.Settings != nil || len(parsed.Errors) == 0 {
			t.Fatalf("invalid statusLine should report schema errors: %s %v %+v", invalid, err, parsed)
		}
	}

	service, _, path := testService(t, nil, nil)
	service.options.Profile = NativeProcessProfile
	service.options.ConfigHome = filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := service.ParseFile(path)
	if err != nil || parsed.Settings == nil || string(parsed.Settings.Get("statusLine")) != `{"type":"command","command":"printf ready","padding":2}` {
		t.Fatalf("native profile read statusLine: %v %+v", err, parsed)
	}
	patch := object(t, `{"statusLine":{"type":"command","command":"printf updated","padding":4}}`)
	if _, err = service.UpdateEditableSource("userSettings", "", Patch{Object: patch}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !containsJSONField(string(stored), `"statusLine"`, `"command": "printf updated"`) {
		t.Fatalf("statusLine update was not persisted: %s %v", stored, err)
	}
}

func containsJSONField(raw, field, expected string) bool {
	return strings.Contains(raw, field) && strings.Contains(raw, expected)
}
