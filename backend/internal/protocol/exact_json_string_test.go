package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestExactJavaScriptStringActualPinnedLanguageInputs(t *testing.T) {
	raw, err := os.ReadFile("testdata/exact_json_string.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name     string  `json:"name"`
			RawJSON  *string `json:"rawJSON"`
			Expected struct {
				ValueJSON string                          `json:"valueJSON"`
				Units     []uint16                        `json:"units"`
				Error     *struct{ Name, Message string } `json:"error"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			var input json.RawMessage
			if fixture.RawJSON != nil {
				input = []byte(*fixture.RawJSON)
			}
			before := append(json.RawMessage(nil), input...)
			result, err := ExactJavaScriptStringJSON(input)
			if !reflect.DeepEqual(before, input) {
				t.Fatal("String conversion mutated the original raw JSON")
			}
			if fixture.Expected.Error != nil {
				var failure *ExactJSONStringTypeError
				if result != nil || !errors.As(err, &failure) || failure.Message != fixture.Expected.Error.Message || failure.Error() != failure.Message || fixture.Expected.Error.Name != "TypeError" {
					t.Fatalf("coercion acquired text instead of the original failure: %s, %v", result, err)
				}
				return
			}
			if err != nil || string(result) != fixture.Expected.ValueJSON {
				t.Fatalf("String raw result = %s, %v; want %s", result, err, fixture.Expected.ValueJSON)
			}
			units, err := DecodeJavaScriptJSONString(result)
			if err != nil || !reflect.DeepEqual(append([]uint16{}, units...), fixture.Expected.Units) {
				t.Fatalf("String lost original UTF-16 units: %x, %v", units, err)
			}
		})
	}
}

func TestExactJavaScriptStringRejectsMalformedRawWithoutText(t *testing.T) {
	for _, raw := range []string{" ", "{", "null true", "[1,]", "undefined", `"unterminated`} {
		if text, err := ExactJavaScriptStringJSON(json.RawMessage(raw)); err == nil || text != nil {
			t.Fatalf("invalid original JSON gained model-visible text: %q -> %s, %v", raw, text, err)
		}
	}
}
