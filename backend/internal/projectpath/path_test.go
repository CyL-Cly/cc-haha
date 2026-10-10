package projectpath

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualOriginalSource75UsesRawUTF16AndOriginalHash(t *testing.T) {
	inputs, err := os.ReadFile("testdata/original75-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/original75-source-output.json")
	if err != nil {
		t.Fatal(err)
	}
	var original []struct {
		ID   string          `json:"id"`
		Name json.RawMessage `json:"name"`
	}
	var source []struct {
		ID        string `json:"id"`
		Units     int    `json:"utf16Units"`
		Decimal   string `json:"nativeHashDecimal"`
		Base36    string `json:"nativeHashBase36"`
		Sanitized string `json:"sanitized"`
	}
	if err = json.Unmarshal(inputs, &original); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(golden, &source); err != nil {
		t.Fatal(err)
	}
	if len(original) != 75 || len(source) != len(original) {
		t.Fatal("actual Source vector count", len(original), len(source))
	}
	for index, row := range original {
		t.Run(row.ID, func(t *testing.T) {
			units, err := protocol.DecodeJavaScriptJSONString(row.Name)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]uint16(nil), units...)
			value, err := SanitizeJSON(row.Name)
			if err != nil {
				t.Fatal(err)
			}
			want := source[index]
			hash := wyhash(0, []byte(string(utf16.Decode(units))))
			if row.ID != want.ID || len(units) != want.Units || strconv.FormatUint(hash, 10) != want.Decimal || strconv.FormatUint(hash, 36) != want.Base36 || value != want.Sanitized {
				t.Fatalf("original %s independent actual %q hash%d expected %q hash%s", row.ID, value, hash, want.Sanitized, want.Decimal)
			}
			if len(units) > 0 && !reflect.DeepEqual(before, units) {
				t.Fatal("input units mutated")
			}
		})
	}
}

func TestSanitizeUnitThresholdAndHashOriginalNFD(t *testing.T) {
	for _, length := range []int{199, 200, 201} {
		units := append(utf16.Encode([]rune(strings.Repeat("A", length-2))), 0xd83d, 0xde00)
		got := SanitizeUTF16(units)
		if length <= 200 && got != strings.Repeat("A", length-2)+"--" || length == 201 && (!strings.HasPrefix(got, strings.Repeat("A", 199)+"--") || len(got) <= 200) {
			t.Fatalf("UTF16 threshold %d actual %s", length, got)
		}
	}
	nfd, nfc := strings.Repeat("e\u0301", 110), strings.Repeat("é", 220)
	if Sanitize(nfd) == Sanitize(nfc) {
		t.Fatal("original raw string was normalized before hash")
	}
	if got := Sanitize("中文/😀-ABC_123"); got != "------ABC-123" {
		t.Fatalf("original replacement actual %q", got)
	}
}

func TestSanitizeJSONRejectsNonStringAndMalformedInputs(t *testing.T) {
	for _, raw := range []string{"", "null", "123", "{}", "[]", `"bad\uX000"`, `"unterminated`} {
		if _, err := SanitizeJSON(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid original string admitted %q", raw)
		}
	}
	units := []uint16{0xd800, 'A', 0xdfff}
	before := append([]uint16(nil), units...)
	if got := SanitizeUTF16(units); got != "-A-" || !reflect.DeepEqual(units, before) {
		t.Fatal("raw UTF16 replacement changed original", got, units)
	}
}
