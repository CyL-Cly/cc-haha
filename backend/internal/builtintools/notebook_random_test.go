package builtintools

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNotebookActualSourceRadix36CellIDs(t *testing.T) {
	data, err := os.ReadFile("testdata/native_notebook_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Randoms []struct {
			Value  float64
			CellID string
		}
	}
	if err = json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Randoms) != 110 {
		t.Fatal("incomplete Source random corpus")
	}
	for _, row := range corpus.Randoms {
		got, err := notebookCellID(row.Value)
		if err != nil || got != row.CellID {
			t.Errorf("Math.random %.17g: %q != Source %q (%v)", row.Value, got, row.CellID, err)
		}
	}
}
