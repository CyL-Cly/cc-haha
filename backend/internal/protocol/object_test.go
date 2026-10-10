package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONObjectPreservesInsertionAndUTF16Identity(t *testing.T) {
	var object JSONObject
	raw := []byte(`{"z":1e400,"\ud800":"high","\udfff":"low","�":"replacement","2":2,"0":0,"01":1,"4294967294":4,"4294967295":5,"a":6,"\u0061":7,"emoji😀":8,"emoji\ud83d\ude00":9}`)
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object.Set("z", json.RawMessage(`2`))
	object.Set("new", json.RawMessage(`"\ud800"`))
	encoded, err := object.MarshalJSON()
	want := []byte(`{"0":0,"2":2,"4294967294":4,"z":2,"\ud800":"high","\udfff":"low","�":"replacement","01":1,"4294967295":5,"a":7,"emoji😀":9,"new":"\ud800"}`)
	if err != nil || !bytes.Equal(encoded, want) {
		t.Fatalf("want=%s actual=%s err=%v", want, encoded, err)
	}
}

func TestJSONObjectCloneDeleteAndUndefinedDoNotMutateHistory(t *testing.T) {
	object := NewJSONObject(JSONField{Name: "type", Value: json.RawMessage(`"text"`)}, JSONField{Name: "text", Value: json.RawMessage(`"before"`)}, JSONField{Name: "future", Value: json.RawMessage(`{"raw":1e400}`)})
	copy := object.Clone()
	value := copy.Get("text")
	value[1] = 'X'
	copy.Set("text", json.RawMessage(`"after"`))
	copy.Delete("type")
	copy.Delete("missing")
	copy.Set("undefined", nil)
	copy.Set("null", json.RawMessage(`null`))
	copy.Set("type", json.RawMessage(`"text"`))
	copy.Delete("future")
	encoded, err := copy.MarshalJSON()
	if err != nil || string(encoded) != `{"text":"after","null":null,"type":"text"}` {
		t.Fatalf("changed=%s err=%v", encoded, err)
	}
	unchanged, err := object.MarshalJSON()
	if err != nil || string(unchanged) != `{"type":"text","text":"before","future":{"raw":1e400}}` {
		t.Fatalf("history mutated=%s err=%v", unchanged, err)
	}
}

func TestJSONObjectRejectsMalformedInputAtomicallyAndInvalidFieldAtEncoding(t *testing.T) {
	object := NewJSONObject(JSONField{Name: "keep", Value: json.RawMessage(`true`)})
	for _, raw := range []string{`{`, `[]`, `42`, `true`, `"text"`, `{} {}`} {
		if err := json.Unmarshal([]byte(raw), &object); err == nil {
			t.Fatal("accepted malformed object", raw)
		}
		if string(object.Get("keep")) != "true" {
			t.Fatal("failed decode mutated object")
		}
	}
	var absent *JSONObject
	if err := absent.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("accepted nil receiver")
	}
	object.Set("broken", json.RawMessage(`{`))
	if _, err := object.MarshalJSON(); err == nil {
		t.Fatal("accepted invalid raw field")
	}
}

func TestJSONObjectNullAndEmptyRemainDifferent(t *testing.T) {
	var object JSONObject
	if object.IsObject() || object.Clone().IsObject() || object.Get("type") != nil {
		t.Fatal("zero object became a record")
	}
	encoded, _ := object.MarshalJSON()
	if string(encoded) != "null" {
		t.Fatal(string(encoded))
	}
	object.Set("undefined", nil)
	encoded, _ = object.MarshalJSON()
	if string(encoded) != "{}" || !object.IsObject() {
		t.Fatal(string(encoded))
	}
	if err := json.Unmarshal([]byte(`null`), &object); err != nil || object.IsObject() {
		t.Fatal(object, err)
	}
	object.Delete("missing")
}

func TestJSONObjectAssignPreservesFactoryUndefinedSlotsAndUnknownUTF16Keys(t *testing.T) {
	factory := NewJSONObject(JSONField{Name: "installMethod"}, JSONField{Name: "autoUpdates"}, JSONField{Name: "known", Value: json.RawMessage(`true`)})
	var stored JSONObject
	if err := stored.UnmarshalJSON([]byte(`{"later":1,"\ud800":2,"�":3,"2":"two","1":"one","known":false,"installMethod":"native"}`)); err != nil {
		t.Fatal(err)
	}
	factory.Assign(stored)
	factory.Set("autoUpdates", json.RawMessage(`false`))
	got, err := factory.MarshalJSON()
	want := `{"1":"one","2":"two","installMethod":"native","autoUpdates":false,"known":false,"later":1,"\ud800":2,"�":3}`
	if err != nil || string(got) != want {
		t.Fatalf("got %s, err %v; want %s", got, err, want)
	}
	undefined := NewJSONObject(JSONField{Name: "known"}, JSONField{Name: "future"})
	factory.Assign(undefined)
	factory.Set("future", json.RawMessage(`"assigned later"`))
	factory.Set("known", json.RawMessage(`null`))
	got, err = factory.MarshalJSON()
	if err != nil || string(got) != `{"1":"one","2":"two","installMethod":"native","autoUpdates":false,"known":null,"later":1,"\ud800":2,"�":3,"future":"assigned later"}` {
		t.Fatalf("undefined assign lost own property slots: %s, %v", got, err)
	}
	before, _ := stored.MarshalJSON()
	factory.Assign(factory)
	factory.Assign(JSONObject{})
	after, _ := stored.MarshalJSON()
	if !bytes.Equal(before, after) {
		t.Fatal("assignment mutated its source")
	}
}
