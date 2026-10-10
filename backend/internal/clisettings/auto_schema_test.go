package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"testing"
)

func TestActualSourceAutoSettingsSchemas(t *testing.T) {
	raw, e := os.ReadFile("testdata/auto-schema-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Original string
		Source   struct {
			Success bool
			Data    json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		r, e := ValidateJSONContent(row.Original, "private settings")
		if e != nil {
			t.Fatal(e)
		}
		if row.Source.Success {
			if r.Settings == nil {
				t.Fatal("Source valid got errors", r.Errors)
			}
			actual, e := r.Settings.MarshalJSON()
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(row.Source.Data)) {
				t.Fatalf("Source=%s Go=%s", row.Source.Data, actual)
			}
		} else if r.Settings != nil || len(r.Errors) == 0 {
			t.Fatalf("Source invalid admitted %+v", r)
		}
	}
}
