package usercontext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestActualSourceOriginalContextCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input    fixtureInput
		Expected json.RawMessage
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.Input.Name, func(t *testing.T) {
			home := t.TempDir()
			row.Input.Home = home
			actual := strings.ReplaceAll(encodedFixture(runFixture(row.Input)), home, "<HOME>")
			var expected, received any
			if err = json.Unmarshal(row.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(actual), &received); err != nil {
				t.Fatal(err)
			}
			if encodedFixture(expected) != encodedFixture(received) {
				t.Fatalf("actual Source corpus changed\nwant %s\n got %s", encodedFixture(expected), actual)
			}
		})
	}
}
