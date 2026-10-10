package clisettings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeDesktopFieldsMatchActualSourceSettingsSchema(t *testing.T) {
	data, err := os.ReadFile("testdata/native_desktop_fields_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Observations []struct {
			Scenario    struct{ Name, Content string }
			Observation struct {
				SettingsSource *string
				ErrorsSource   string
			}
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 12 || !strings.Contains(corpus.SourceOnly.Consumer, "SettingsSchema") || len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("missing actual Source settings capture")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			parsed, err := ValidateNativeJSONContent(row.Scenario.Content, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if row.Observation.SettingsSource == nil {
				if parsed.Settings != nil {
					t.Fatal("Source invalid settings were retained")
				}
			} else {
				if parsed.Settings == nil {
					t.Fatalf("Source valid settings rejected: %s", rawJSON(parsed.Errors))
				}
				got, _ := parsed.Settings.MarshalJSON()
				if got, want := protocol.StringifyValidatedJavaScriptJSON(got), protocol.StringifyValidatedJavaScriptJSON([]byte(*row.Observation.SettingsSource)); string(got) != string(want) {
					t.Fatalf("Source fields/order/UTF16/unknown-field behavior changed: %s != %s", got, want)
				}
			}
			if got, want := protocol.StringifyValidatedJavaScriptJSON(rawJSON(parsed.Errors)), protocol.StringifyValidatedJavaScriptJSON([]byte(row.Observation.ErrorsSource)); string(got) != string(want) {
				t.Fatalf("Source validation changed: %s != %s", got, want)
			}
		})
	}
	for _, content := range []string{`{"outputStyle":"Explanatory"}`, `{"respectGitignore":false}`} {
		if _, err := ValidateJSONContent(content, "finite"); err == nil {
			t.Fatal("original finite field guard broadened")
		}
	}
}

func rawJSON(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
