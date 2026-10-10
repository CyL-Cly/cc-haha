package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"testing"
)

func TestNativeModelFieldsMatchActualSourceAndRetainFiniteGuards(t *testing.T) {
	data, err := os.ReadFile("testdata/native_model_fields_contract.json")
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
		SourceOnly struct{ Capabilities struct{ Blocked []string } }
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 11 || len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("missing actual Source schema capture")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			parsed, err := ValidateNativeJSONContent(row.Scenario.Content, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if row.Observation.SettingsSource == nil {
				if parsed.Settings != nil {
					t.Fatal("invalid settings retained")
				}
			} else {
				if parsed.Settings == nil {
					t.Fatalf("valid settings rejected %s", rawJSON(parsed.Errors))
				}
				got, _ := parsed.Settings.MarshalJSON()
				if string(protocol.StringifyValidatedJavaScriptJSON(got)) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(*row.Observation.SettingsSource))) {
					t.Fatalf("settings/order/UTF16 %s want %s", got, *row.Observation.SettingsSource)
				}
			}
			if string(protocol.StringifyValidatedJavaScriptJSON(rawJSON(parsed.Errors))) != string(protocol.StringifyValidatedJavaScriptJSON([]byte(row.Observation.ErrorsSource))) {
				t.Fatalf("error %s want %s", rawJSON(parsed.Errors), row.Observation.ErrorsSource)
			}
		})
	}
	for _, content := range []string{`{"availableModels":[]}`, `{"modelOverrides":{}}`} {
		if _, err := ValidateJSONContent(content, "finite"); err == nil {
			t.Fatal("finite model field widened")
		}
	}
}
