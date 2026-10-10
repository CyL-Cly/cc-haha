package retrypolicy_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy/testdata/attempt-fixture/driver"
)

func TestActualSourceAttemptContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/attempt_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input       driver.Input    `json:"input"`
		Observation json.RawMessage `json:"observation"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 82 {
		t.Fatal("actual Source corpus is incomplete")
	}
	names := map[string]bool{}
	for _, entry := range cases {
		if entry.Input.Name == "" || names[entry.Input.Name] {
			t.Fatal("actual Source corpus contains a missing or repeated profile")
		}
		names[entry.Input.Name] = true
		t.Run(entry.Input.Name, func(t *testing.T) {
			actual, err := json.Marshal(driver.Observe(entry.Input))
			if err != nil {
				t.Fatal(err)
			}
			var expected, value any
			if err = json.Unmarshal(entry.Observation, &expected); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(actual, &value); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, value) {
				t.Fatalf("Source=%s\nGo=%s", entry.Observation, actual)
			}
		})
	}
}
