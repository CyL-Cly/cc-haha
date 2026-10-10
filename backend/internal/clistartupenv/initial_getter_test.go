package clistartupenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInitialSettingsActualSource(t *testing.T) {
	data, err := os.ReadFile("testdata/initial_getter_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    fixtureInput
		Expected json.RawMessage
	}
	if err = json.Unmarshal(data, &cases); err != nil || len(cases) != 8 {
		t.Fatal(err, len(cases))
	}
	for _, row := range cases {
		t.Run(row.Input.Name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			row.Input.Home = home
			got, err := executeInitialGetterFixture(row.Input)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.ReplaceAll(string(raw), home, "<HOME>"))
			var actual, expected any
			if err = json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(row.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			// Observation objects have declared fields, while every nested
			// ordered settings string, own-slot array and effect is exact.
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("Source getter mismatch\nactual %s\nexpected %s", raw, row.Expected)
			}
		})
	}
}

func TestInitialGetterCapabilityAndFrozenHost(t *testing.T) {
	t.Run("active schema", func(t *testing.T) {
		service, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
		err := os.WriteFile(filepath.Join(service.options.ConfigHome, "settings.json"), []byte(`{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"PRIVATE_NEVER_EXECUTED"}]}]}}`), 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.GetInitialSettings()
		if _, ok := err.(*clisettings.CapabilityError); !ok {
			t.Fatal("active hook schema must remain a capability", err)
		}
	})
	t.Run("original host changed", func(t *testing.T) {
		service, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
		if err := service.options.Environment.SetProperty(quote("USER_TYPE"), quote("ant")); err != nil {
			t.Fatal(err)
		}
		_, err := service.GetInitialSettings()
		if _, ok := err.(*CapabilityError); !ok {
			t.Fatal("unresolved changed host accepted", err)
		}
		if service.options.SettingsState.GetSessionSettingsCache() != nil {
			t.Fatal("guard populated cache")
		}
	})
}
