package uipreferences

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUTF16WhitespaceSlicesBoundsAndFutureSchema(t *testing.T) {
	raw := `{"schemaVersion":999,"profile":{"displayName":"` + strings.Repeat("x", 79) + `😀","subtitle":"` + strings.Repeat("x", 159) + `😀"},"pet":{"size":1e400,"lastSessionId":"` + strings.Repeat("x", 199) + `😀"},"voiceInput":{"providerId":"` + strings.Repeat("x", 79) + `😀"},"future":1e400}`
	value, valid, err := normalize(json.RawMessage(raw))
	if err != nil || !valid {
		t.Fatal(err)
	}
	profile, _ := parseObject(field(t, value, "profile"))
	pet, _ := parseObject(field(t, value, "pet"))
	voice, _ := parseObject(field(t, value, "voiceInput"))
	for _, pair := range []struct {
		raw    json.RawMessage
		length int
	}{{profile.get("displayName"), 80}, {profile.get("subtitle"), 160}, {pet.get("lastSessionId"), 200}} {
		units, valid := units(pair.raw)
		if !valid || len(units) != pair.length || units[len(units)-1] != 0xd83d {
			t.Fatal("UTF16 slice was rune based", string(pair.raw))
		}
	}
	if string(pet.get("size")) != "144" || !stringIs(voice.get("providerId"), "sensevoice-local") || string(field(t, value, "schemaVersion")) != "999" {
		t.Fatal(value)
	}
	encoded, err := value.MarshalJSON()
	if err != nil || !strings.Contains(string(encoded), `"future":null`) {
		t.Fatal(string(encoded), err)
	}
	for _, schema := range []string{`1e400`, `9007199254740993`, `6.5`, `null`, `"999"`, `-1`} {
		value, _, _ := normalize(json.RawMessage(`{"schemaVersion":` + schema + `}`))
		if string(field(t, value, "schemaVersion")) != "6" {
			t.Fatal("invalid future schema retained", schema)
		}
	}
	if got := string(normalizedText(quoted("\ufeffA\n\tB\ufeff"), "fallback", 80)); got != `"A B"` {
		t.Fatal(got)
	}
	if got := string(normalizedText(quoted("\u0085"), "fallback", 80)); got != `"\u0085"` && got != string(quoted("\u0085")) {
		t.Fatal("NEL treated as JS whitespace", got)
	}
}

func TestEntryLimitsIntegerKeyEnumerationDuplicatesAndInvalidSections(t *testing.T) {
	array := []string{}
	for index := 0; index < 2005; index++ {
		array = append(array, string(quoted(strconvName(index))))
	}
	if got := normalizedStringArray(json.RawMessage("[" + strings.Join(array, ",") + "]")); strings.Count(string(got), ",") != 1999 {
		t.Fatal("array cap drift")
	}
	names := object{}
	names.set("other", quoted("Other"))
	for index := 2001; index >= 0; index-- {
		names.set(strconvName(index), quoted("name"))
	}
	result := normalizeProjectNames(names.raw())
	if len(result) != 2000 || result[0].key != `"0"` || result[len(result)-1].key != `"1999"` {
		t.Fatal("JS integer order/cap drift")
	}
	object, valid := parseObject(json.RawMessage(`{"\ud800":1,"\ud801":2,"z":0,"z":3,"2":"two","1":"one"}`))
	if !valid || len(object) != 5 || object[0].key != `"1"` || object[2].key != `"\ud800"` || string(object.get("z")) != "3" {
		t.Fatal(object)
	}
	for _, raw := range []string{`null`, `[]`, `1`, `"wrong"`} {
		value, valid, _ := normalize(json.RawMessage(raw))
		if valid || value != nil {
			t.Fatal("invalid root accepted", raw)
		}
	}
	value, _, _ := normalize(json.RawMessage(`{"sidebar":null,"profile":[],"pet":{"size":12.5,"motionEnabled":0,"selectedPetId":"BAD","lastSessionId":" "},"voiceInput":{"providerId":null},"projectDisplayNames":{" ":"x","key":" ","long":"` + strings.Repeat("x", 81) + `","number":1}}`))
	pet, _ := parseObject(field(t, value, "pet"))
	if string(pet.get("size")) != "144" || string(pet.get("lastSessionId")) != "null" || string(field(t, value, "projectDisplayNames")) != "{}" {
		t.Fatal(value)
	}
	var preferences Preferences
	if err := json.Unmarshal([]byte(`{broken`), &preferences); err == nil {
		t.Fatal("invalid preferences JSON accepted")
	}
	if _, err := Preferences(`{broken`).MarshalJSON(); err == nil {
		t.Fatal("invalid preferences stringify accepted")
	}
}
