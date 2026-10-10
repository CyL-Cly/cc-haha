package sdkoutput_test

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput/testdata/program"
	"os"
	"testing"
)

func TestActualSourceOriginalProgramReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-programs.json")
	if err != nil {
		t.Fatal(err)
	}
	var input program.Input
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("testdata/source-assertions.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Results []program.Result `json:"results"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	actual, err := program.Run(input)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range expected.Results {
		t.Run(want.Name, func(t *testing.T) {
			got := actual[index]
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if !bytes.Equal(a, b) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(want)
				t.Fatalf("actual=%s\nSource=%s", a, b)
			}
		})
	}
}
