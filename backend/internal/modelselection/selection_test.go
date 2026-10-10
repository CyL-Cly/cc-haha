package modelselection

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDirectCredentialSelectionMatchesActualSource(t *testing.T) {
	raw, err := os.ReadFile("testdata/actual_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Rows []struct {
			Scenario struct {
				Name     string
				Env      map[string]string
				Settings protocol.JSONObject
				Override json.RawMessage
			}
			Observation struct {
				SelectedJSON, DefaultJSON string
				Parsed                    []struct {
					Input     json.RawMessage
					ValueJSON string
				}
				Allowed []struct {
					Input json.RawMessage
					Value bool
				}
			}
		}
		SourceOnly struct {
			Consumer, Runtime string
			Capabilities      struct{ Blocked []string }
		}
	}
	if err = json.Unmarshal(raw, &corpus); err != nil || len(corpus.Rows) != 28 {
		t.Fatalf("actual Source corpus: %v rows=%d", err, len(corpus.Rows))
	}
	if corpus.SourceOnly.Consumer != "src/utils/model/model.ts + modelAllowlist.ts" || len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("missing original Source provenance/guard")
	}
	for _, row := range corpus.Rows {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			env := map[string]string{"ANTHROPIC_API_KEY": "fake-model-selection-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}
			for key, value := range row.Scenario.Env {
				env[key] = value
			}
			selector, err := NewDirectCredentials(Options{Environment: env, Settings: row.Scenario.Settings, Override: row.Scenario.Override})
			if err != nil {
				t.Fatal(err)
			}
			selected, err := selector.Select()
			if err != nil || string(selected) != row.Observation.SelectedJSON {
				t.Fatalf("select got %s error=%v want %s", selected, err, row.Observation.SelectedJSON)
			}
			defaultSelector, err := NewDirectCredentials(Options{Environment: env, Settings: row.Scenario.Settings, Override: []byte("null")})
			if err != nil {
				t.Fatal(err)
			}
			selected, err = defaultSelector.Select()
			if err != nil || string(selected) != row.Observation.DefaultJSON {
				t.Fatalf("default got %s error=%v want %s", selected, err, row.Observation.DefaultJSON)
			}
			for _, p := range row.Observation.Parsed {
				got, err := selector.Parse(p.Input)
				if err != nil || string(got) != p.ValueJSON {
					t.Fatalf("parse %q got %s error=%v want %s", p.Input, got, err, p.ValueJSON)
				}
			}
			for _, a := range row.Observation.Allowed {
				got, err := selector.Allowed(a.Input)
				if err != nil || got != a.Value {
					t.Fatalf("allow %q got %v error=%v want %v", a.Input, got, err, a.Value)
				}
			}
		})
	}
}
func TestRawSettingsIdentityAndOriginalInputs(t *testing.T) {
	var settings protocol.JSONObject
	if err := settings.UnmarshalJSON([]byte(`{"model":" \ud800Model\udfff ","availableModels":["\ud800Model\udfff"],"modelOverrides":{"unknown":"\udfff"}}`)); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "fake", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}
	before, _ := settings.MarshalJSON()
	s, e := NewDirectCredentials(Options{Environment: env, Settings: settings})
	if e != nil {
		t.Fatal(e)
	}
	env["ANTHROPIC_MODEL"] = "observer mutation"
	settings.Set("model", []byte(`"observer mutation"`))
	got, e := s.Select()
	if e != nil || !bytes.Equal(got, []byte(`"\ud800Model\udfff"`)) {
		t.Fatalf("raw UTF16 input changed: %s %v original=%s", got, e, before)
	}
}
func TestUnresolvedAuthProfilesRemainExplicit(t *testing.T) {
	for _, env := range []map[string]string{{}, {"ANTHROPIC_API_KEY": "fake", "USER_TYPE": "ant"}, {"ANTHROPIC_API_KEY": "fake", "CLAUDE_CODE_REMOTE": "true"}, {"ANTHROPIC_AUTH_TOKEN": "fake", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}} {
		_, err := NewDirectCredentials(Options{Environment: env, Settings: protocol.NewJSONObject()})
		var cap *CapabilityError
		if !errors.As(err, &cap) {
			t.Fatal("unknown auth profile acquired defaults", err)
		}
	}
}

func TestCommandModelParsingDoesNotRequireRequestCredentials(t *testing.T) {
	selector, err := NewParsing(Options{Environment: map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "TeamModel"}, Settings: protocol.NewJSONObject()})
	if err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{`"sonnet"`: `"TeamModel"`, `" Sonnet[1m] "`: `"TeamModel[1m]"`, `" CaseModel "`: `"CaseModel"`} {
		got, err := selector.Parse(json.RawMessage(input))
		if err != nil || string(got) != want {
			t.Fatalf("parse %s=%s want=%s err=%v", input, got, want, err)
		}
	}
	if _, err := NewDirectCredentials(Options{Environment: map[string]string{}, Settings: protocol.NewJSONObject()}); err == nil {
		t.Fatal("metadata discovery admitted a credential-free model request")
	}
}
