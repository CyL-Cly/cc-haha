package clisettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationFieldsActualSourceGoldens(t *testing.T) {
	data, err := os.ReadFile("../climigrations/testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input    struct{ Name, Schema string }
		Expected struct{ Result json.RawMessage }
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Input.Schema == "" {
			continue
		}
		t.Run(row.Input.Name, func(t *testing.T) {
			parsed, err := ValidateJSONContent(row.Input.Schema, "<HOME>/.claude/settings.json")
			if err != nil {
				t.Fatal(err)
			}
			var settings any
			if parsed.Settings != nil {
				raw, _ := parsed.Settings.MarshalJSON()
				settings = string(protocol.StringifyValidatedJavaScriptJSON(raw))
			}
			raw, _ := json.Marshal(map[string]any{"success": parsed.Settings != nil, "settings": settings, "errors": parsed.Errors})
			if !bytes.Equal(canonicalObservation(raw), canonicalObservation(row.Expected.Result)) {
				t.Fatalf("actual Source schema mismatch\nGo=%s\nSource=%s", raw, row.Expected.Result)
			}
		})
	}
}
func TestMigrationSchemaDoesNotAdmitRulesOrFilterArrays(t *testing.T) {
	for _, raw := range []string{`{"permissions":{"allow":[]}}`, `{"permissions":{"deny":["Bash"]}}`, `{"permissions":{"ask":null}}`, `{"permissions":[]}`} {
		if _, err := ValidateJSONContent(raw, "fixture"); err == nil {
			t.Fatal("unported permission grammar admitted", raw)
		}
	}
}
func TestLocalGitBoundaryBeforePhysicalWrites(t *testing.T) {
	service, state, _ := testService(t, nil, nil)
	root := service.options.OriginalCWD
	_ = os.Mkdir(filepath.Join(root, ".git"), 0777)
	_, err := service.UpdateEditableSource("localSettings", "", Patch{Object: object(t, `{"enabledMcpjsonServers":["x"]}`)})
	var capability *CapabilityError
	if !errors.As(err, &capability) || state.ConsumeInternalWrite(filepath.Join(root, ".claude", "settings.local.json"), 5000) {
		t.Fatal("Git branch silently bypassed", err)
	}
	if _, err = service.Path("localSettings", "relative"); err == nil {
		t.Fatal("unresolved local path accepted")
	}
}
func TestWriterErrorDiagnosticsAreOrderedAndOptIn(t *testing.T) {
	service, _, path := testService(t, nil, nil)
	path = filepath.Join(service.options.OriginalCWD, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0777)
	_ = os.WriteFile(path, []byte("{"), 0600)
	messages := []json.RawMessage{}
	service.options.Diagnostic = func(raw json.RawMessage) { messages = append(messages, raw) }
	result, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)})
	if err != nil || result.Error == nil || len(messages) != 1 || string(messages[0]) != `"SyntaxError: JSON Parse error: Expected '}'"` {
		t.Fatal(result, err, messages)
	}
	service.options.State.ResetSettingsCache()
	_ = os.WriteFile(path, []byte("null"), 0600)
	messages = nil
	_, err = service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)})
	if err != nil || len(messages) != 0 {
		t.Fatal("valid null manufactured syntax error", err, messages)
	}
}
