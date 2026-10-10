package migrations

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrderedObjectRetainsDuplicateKeyPositionUTF16AndJSNumbers(t *testing.T) {
	raw := `{"z":0,"\ud800":1,"\ud801":2,"z":9007199254740993,"2":"second","1":"first","negative":-0,"overflow":1e400,"small":1e-400,"html":"<>&"}`
	value, valid := parseObject(json.RawMessage(raw))
	if !valid {
		t.Fatal("valid object rejected")
	}
	if len(value) != 9 || string(value.get("z")) != "9007199254740993" {
		t.Fatal(value)
	}
	if got := stable(t, string(value.raw())); got != stable(t, `{"1":"first","2":"second","z":9007199254740992,"\ud800":1,"\ud801":2,"negative":0,"overflow":null,"small":0,"html":"<>&"}`) {
		t.Fatal(got)
	}
	for _, raw := range []string{`null`, `[]`, `"value"`, ``, `{"broken":`, `{"x":1`, `{"x":1,`} {
		if _, valid := parseObject(json.RawMessage(raw)); valid {
			t.Fatal("invalid record accepted", raw)
		}
	}
	if _, valid := parseArray(json.RawMessage(`{"0":"x"}`)); valid {
		t.Fatal("object treated as array")
	}
	if _, err := stableJSON(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid stringify succeeded")
	}
}

func TestProviderSchemaDecisionPrecedesStringifyOverflowAndFutureVersion(t *testing.T) {
	for _, version := range []string{"1e400", "999", "4", "3", "null", "\"6\""} {
		raw := `{"schemaVersion":` + version + `,"providers":[` + oldProvider + `],"activeId":"claude-official","future":9007199254740993}`
		migrated, err := migrateProviders(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		value, _ := parseObject(migrated)
		providers, _ := parseArray(value.get("providers"))
		provider, _ := parseObject(providers[0])
		wantDisabled := version == "3" || version == "null" || version == `"6"`
		if (provider.get("toolSearchEnabled") != nil) != wantDisabled {
			t.Fatalf("schema %s prematurely normalized before comparison", version)
		}
		if string(value.get("schemaVersion")) != "6" || string(value.get("activeId")) != "null" {
			t.Fatal(value)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"providers":null}`} {
		migrated, _ := migrateProviders(json.RawMessage(raw))
		if !strings.Contains(string(migrated), `"officialProviderModels":{}`) {
			t.Fatal(migrated)
		}
	}
	for _, id := range []string{"openai-official", "grok-official"} {
		migrated, _ := migrateProviders(json.RawMessage(`{"providers":[],"activeId":"` + id + `"}`))
		value, _ := parseObject(migrated)
		if !equalStrings(value.get("activeId"), quote(id)) {
			t.Fatal(id)
		}
	}
}

func TestManagedRuntimeShapeAndJSCoercionDecisions(t *testing.T) {
	for _, test := range []struct {
		raw             string
		changed, failed bool
	}{
		{`null`, true, false},
		{`{"future":true}`, false, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_BASE_URL":["api.deepseek.com"]}}`, true, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":["DEEPSEEK-x"]}}`, true, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":{}}}`, false, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":true}}`, false, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":0}}`, false, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_BASE_URL":1}}`, false, true},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":{"toString":null}}}`, false, true},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":[{"toString":null}]}}`, false, true},
	} {
		migrated, err := migrateManaged(json.RawMessage(test.raw))
		if (err != nil) != test.failed {
			t.Fatalf("%s: %v", test.raw, err)
		}
		if err == nil && (stable(t, string(migrated)) != stable(t, test.raw)) != test.changed {
			t.Fatal(test.raw, string(migrated))
		}
	}
}

func TestSavedAndLegacyValidationAndActiveSelection(t *testing.T) {
	if validSavedProvider(json.RawMessage(strings.Replace(oldProvider, `"main":"main"`, `"main":"main","fable":null`, 1))) {
		t.Fatal("null Fable accepted")
	}
	if validLegacyProvider(json.RawMessage(`{"id":"x","name":"x","apiKey":"k","baseUrl":"b","models":[null]}`)) {
		t.Fatal("invalid legacy model accepted")
	}
	raw := `{"activeModel":"","providers":[{"id":"a","name":"a","apiKey":"","baseUrl":"b","models":[{"id":""}]},{"id":"b","name":"b","apiKey":"k","baseUrl":"b","isActive":true,"models":[]}]}`
	migrated := migrateLegacy(json.RawMessage(raw))
	value, _ := parseObject(migrated)
	if !equalStrings(value.get("activeId"), quote("a")) || managedForLegacy(migrated) != nil {
		t.Fatal("first matching provider / empty credential changed")
	}
	for _, raw := range []string{`null`, `{"providers":true}`, `{"providers":[]}`} {
		if migrateLegacy(json.RawMessage(raw)) != nil {
			t.Fatal("empty legacy configuration migrated", raw)
		}
	}
	if managedForLegacy(json.RawMessage(`{"providers":[],"activeId":null}`)) != nil {
		t.Fatal("missing active provider created managed settings")
	}
}
