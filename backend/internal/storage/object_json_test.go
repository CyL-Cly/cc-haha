package storage

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestObjectCodecRetainsDistinctUTF16KeysAndRawValues(t *testing.T) {
	input := []byte(`{"\ud800":1e400,"\udfff":9007199254740993,"�":"replacement","\u0061":1,"a":2,"😀":"first","\ud83d\ude00":"last","env":{"nested":true},"":-0}`)
	var object Object
	if err := json.Unmarshal(input, &object); err != nil {
		t.Fatal(err)
	}
	want := Object{
		"\xed\xa0\x80": json.RawMessage(`1e400`),
		"\xed\xbf\xbf": json.RawMessage(`9007199254740993`),
		"�":            json.RawMessage(`"replacement"`),
		"a":            json.RawMessage(`2`),
		"😀":            json.RawMessage(`"last"`),
		"env":          json.RawMessage(`{"nested":true}`),
		"":             json.RawMessage(`-0`),
	}
	if !reflect.DeepEqual(object, want) {
		t.Fatalf("unknown keys/values changed: %#v", object)
	}
	// Values and names must survive reuse of the transport's input buffer.
	for index := range input {
		input[index] = ' '
	}
	encoded, err := json.Marshal(object)
	if err != nil || !bytes.Contains(encoded, []byte(`"\ud800":1e400`)) || !bytes.Contains(encoded, []byte(`"\udfff":9007199254740993`)) {
		t.Fatalf("UTF16 keys or raw numeric precision disappeared: %s, %v", encoded, err)
	}
	var roundTrip Object
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, want) {
		t.Fatalf("round trip changed keys: %#v, %v", roundTrip, err)
	}
}

func TestObjectCodecKeepsMapMergeNullAndEmptyContracts(t *testing.T) {
	object := Object{"keep": json.RawMessage(`true`), "a": json.RawMessage(`0`)}
	if err := json.Unmarshal([]byte(` { "a":1,"new":null } `), &object); err != nil || string(object["a"]) != "1" || string(object["keep"]) != "true" || string(object["new"]) != "null" {
		t.Fatal(object, err)
	}
	if err := json.Unmarshal([]byte(`{}`), &object); err != nil || len(object) != 3 {
		t.Fatal("empty object erased existing map", object, err)
	}
	if err := json.Unmarshal([]byte(`null`), &object); err != nil || object != nil {
		t.Fatal("null did not reset map", object, err)
	}
	if encoded, err := json.Marshal(object); err != nil || string(encoded) != "null" {
		t.Fatal(encoded, err)
	}
	if err := json.Unmarshal([]byte(`{}`), &object); err != nil || object == nil {
		t.Fatal("empty object decoded as null", object, err)
	}
	if encoded, err := json.Marshal(object); err != nil || string(encoded) != "{}" {
		t.Fatal(encoded, err)
	}
}

func TestObjectDecoderRejectsInvalidDocumentsWithoutPartialMutation(t *testing.T) {
	for _, source := range []string{"", `[]`, `"scalar"`, `true`, `12`, `1e400`, `{`, `{"unfinished`, `{"x":}`, `{"x":1`, `{"x":1]`, `{"x":1} false`, `{"x":1} broken`, `null false`, `null broken`} {
		object := Object{"existing": json.RawMessage(`{"keep":true}`)}
		err := object.UnmarshalJSON([]byte(source))
		if err == nil || len(object) != 1 || string(object["existing"]) != `{"keep":true}` {
			t.Fatalf("invalid input changed existing map: %q, %#v, %v", source, object, err)
		}
	}
	var receiver *Object
	if err := receiver.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil receiver accepted")
	}
}

func TestObjectEncoderQuotesKeysWithoutChangingUnknownJSON(t *testing.T) {
	var object Object
	if err := json.Unmarshal([]byte(`{"key\ud800\n\t\"\\": [1e-400, -0, {"unknown":"<>&"}],"中文😀":true}`), &object); err != nil {
		t.Fatal(err)
	}
	object["nullable"] = nil
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		t.Fatal(err)
	}
	encoded := output.String()
	for _, unchanged := range []string{`"key\ud800\n\t\"\\":[1e-400,-0,{"unknown":"<>&"}]`, `"中文😀":true`, `"nullable":null`} {
		if !strings.Contains(encoded, unchanged) {
			t.Fatalf("unknown data changed: %s; missing %s", encoded, unchanged)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage{}, json.RawMessage(`{`)} {
		encoded, err := (Object{"bad": raw}).MarshalJSON()
		if err == nil || encoded != nil {
			t.Fatal("invalid raw value produced a partial JSON object", encoded, err)
		}
	}
	invalidKeyEncoded, err := (Object{string([]byte{0xff}): json.RawMessage(`1`)}).MarshalJSON()
	if err != nil || string(invalidKeyEncoded) != `{"�":1}` {
		t.Fatal("non-WTF8 invalid string did not use UTF8 replacement", string(invalidKeyEncoded), err)
	}
}
