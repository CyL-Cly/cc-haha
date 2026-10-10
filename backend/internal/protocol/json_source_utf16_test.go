package protocol

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"unicode/utf16"
)

func TestOriginalJavaScriptJSONSourceUTF16(t *testing.T) {
	var rows []struct {
		Name        string   `json:"name"`
		Units       []uint16 `json:"units"`
		Valid       bool     `json:"valid"`
		Stringified *string  `json:"stringified"`
	}
	raw, err := os.ReadFile("testdata/json_source_utf16.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			original := append([]uint16(nil), row.Units...)
			source := JavaScriptJSONSourceUTF16(row.Units)
			if !reflect.DeepEqual(original, row.Units) {
				t.Fatal("source converter mutated original units")
			}
			if json.Valid([]byte(source)) != row.Valid {
				t.Fatalf("parse validity changed: %q", source)
			}
			if row.Valid {
				got, err := ParseAndStringifyJavaScriptJSON([]byte(source))
				if err != nil || row.Stringified == nil || string(got) != *row.Stringified {
					t.Fatalf("parsed value changed: %s, %v", got, err)
				}
			} else {
				got := NormalizeToolArgumentUTF16(row.Units, true)
				var marker struct {
					Marker struct {
						Raw    json.RawMessage `json:"raw"`
						Length int             `json:"len"`
					} `json:"__unparsedToolInput"`
				}
				if err := json.Unmarshal(got, &marker); err != nil {
					t.Fatal(err)
				}
				units, err := DecodeJavaScriptJSONString(marker.Marker.Raw)
				if err != nil || !reflect.DeepEqual(units, row.Units) || marker.Marker.Length != len(row.Units) {
					t.Fatalf("invalid original input became executable or lost identity: %s", got)
				}
				if string(NormalizeToolArgumentUTF16(row.Units, false)) != "{}" {
					t.Fatal("invalid remote input became executable")
				}
			}
		})
	}
}

func TestJSONSourceUTF16DoesNotApplyStringifyCoercions(t *testing.T) {
	for _, source := range []string{"null", "1e400", "-1e400", "-0", "9007199254740993", "  [1e400,-0]  "} {
		if got := JavaScriptJSONSourceUTF16(utf16.Encode([]rune(source))); got != source {
			t.Fatalf("source prematurely normalized: %q -> %q", source, got)
		}
	}
}
