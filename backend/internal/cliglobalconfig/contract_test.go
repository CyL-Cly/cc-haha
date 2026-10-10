package cliglobalconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Captured only by the explicit Source recorder in cli-global-config.test.ts.
// Inputs are neutral initial files and operations; no rendered default/config
// from Source is passed into the Go service's construction or updater.
func TestActualProductionSourceContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Input    json.RawMessage
		Expected json.RawMessage
	}
	if err = json.Unmarshal(raw, &corpus); err != nil || len(corpus) != 86 {
		t.Fatalf("actual Source corpus: %v cases=%d", err, len(corpus))
	}
	for _, item := range corpus {
		var input fixtureInput
		if err = json.Unmarshal(item.Input, &input); err != nil {
			t.Fatal(err)
		}
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(item.Input, &shape)
		_, input.HasRaw = shape["raw"]
		t.Run(input.Name, func(t *testing.T) {
			input.Home = t.TempDir()
			observation, stderr, err := runFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range observation.Results {
				for _, file := range step.Files {
					fields := file.(map[string]any)
					if encoded, ok := fields["rawBase64"].(string); ok {
						raw, failure := base64.StdEncoding.DecodeString(encoded)
						if failure != nil {
							t.Fatal(failure)
						}
						fields["rawBase64"] = base64.StdEncoding.EncodeToString(bytes.ReplaceAll(raw, []byte(input.Home), []byte("<HOME>")))
					}
				}
			}
			got, err := json.Marshal(map[string]any{"observation": observation, "stderr": stderr})
			if err != nil {
				t.Fatal(err)
			}
			// Allocation roots are explicit fixture metadata; embedded ordered
			// JSON/file bytes remain exact strings in the observation.
			got = []byte(strings.ReplaceAll(string(got), input.Home, "<HOME>"))
			var expected, actual any
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(got, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("actual Source contract changed\nwant=%s\ngot=%s", item.Expected, got)
			}
		})
	}
}
