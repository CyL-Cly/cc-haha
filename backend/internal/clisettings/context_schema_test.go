package clisettings

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Same-area consumers use the actual Source file parser recordings. The raw
// original configuration is the input; no parsed Source settings construct Go.
func TestActualSourceContextPluginAndEmptyHookSchema(t *testing.T) {
	data, err := os.ReadFile("../usercontext/testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input struct {
			Name  string
			Steps []struct{ Kind, Raw string }
		}
		Expected struct {
			Results []struct {
				Result struct{ SettingsJSON, ErrorsJSON string }
			}
		}
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		for index, step := range row.Input.Steps {
			if step.Kind != "schema" {
				continue
			}
			t.Run(row.Input.Name, func(t *testing.T) {
				parsed, err := ValidateJSONContent(step.Raw, "<HOME>/.claude/settings.json")
				if err != nil {
					t.Fatal(err)
				}
				settings, err := json.Marshal(parsed.Settings)
				if err != nil {
					t.Fatal(err)
				}
				settings, err = protocol.ParseAndStringifyJavaScriptJSON(settings)
				if err != nil {
					t.Fatal(err)
				}
				errors, err := json.Marshal(parsed.Errors)
				if err != nil {
					t.Fatal(err)
				}
				errors, err = protocol.ParseAndStringifyJavaScriptJSON(errors)
				if err != nil {
					t.Fatal(err)
				}
				expected := row.Expected.Results[index].Result
				if string(settings) != expected.SettingsJSON || string(errors) != expected.ErrorsJSON {
					t.Fatalf("Source parsed projection changed\nwant %s %s\n got %s %s", expected.SettingsJSON, expected.ErrorsJSON, settings, errors)
				}
			})
		}
	}
}
func TestUnportedActiveHookDefinitionAndUnknownEventGuard(t *testing.T) {
	for _, raw := range []string{`{"hooks":{"Stop":[{"hooks":[]}]}}`, `{"hooks":{"Unknown":[]}}`, `{"hooks":{"Stop":null}}`} {
		if _, err := ValidateJSONContent(raw, "fixture"); err == nil {
			t.Fatal("unported hook shape admitted", raw)
		}
	}
}
func TestUnportedActiveHooksStopBeforeWriterMutation(t *testing.T) {
	for _, raw := range []string{`{"hooks":{"Stop":[{"hooks":[]}]}}`, `{"hooks":{"Unknown":[]}}`} {
		effects := []Effect{}
		service, state, path := testService(t, nil, func(effect Effect) { effects = append(effects, effect) })
		patch := object(t, raw)
		if _, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: patch}); err == nil {
			t.Fatal("active hook admitted")
		}
		if len(effects) != 0 || state.ConsumeInternalWrite(path, 5000) {
			t.Fatal("active hook changed writer state")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("active hook wrote file")
		}
	}
}
