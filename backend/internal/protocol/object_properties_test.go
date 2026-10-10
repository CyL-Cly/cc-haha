package protocol

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func TestOwnPropertiesRetainsUndefinedUTF16AndNumericSlots(t *testing.T) {
	var object JSONObject
	if err := json.Unmarshal([]byte(`{"tail":1,"10":2,"2":3,"\ud800":1e400,"replacement":"\ufffd"}`), &object); err != nil {
		t.Fatal(err)
	}
	if err := object.SetPropertyJSON(json.RawMessage(`"missingValue"`), nil); err != nil {
		t.Fatal(err)
	}
	want := []JSONProperty{
		{NameJSON: json.RawMessage(`"2"`), Value: json.RawMessage(`3`)},
		{NameJSON: json.RawMessage(`"10"`), Value: json.RawMessage(`2`)},
		{NameJSON: json.RawMessage(`"tail"`), Value: json.RawMessage(`1`)},
		{NameJSON: json.RawMessage(`"\ud800"`), Value: json.RawMessage(`1e400`)},
		{NameJSON: json.RawMessage(`"replacement"`), Value: json.RawMessage(`"\ufffd"`)},
		{NameJSON: json.RawMessage(`"missingValue"`)},
	}
	if got := object.OwnProperties(); !reflect.DeepEqual(got, want) {
		t.Fatalf("own data properties changed before stringify: %+v", got)
	}
	copy := object.OwnProperties()
	copy[0].NameJSON[1] = '9'
	copy[0].Value[0] = '9'
	if !reflect.DeepEqual(object.OwnProperties(), want) {
		t.Fatal("a returned property snapshot modified the retained record")
	}
	if err := object.SetPropertyJSON(json.RawMessage(`"t\u0061il"`), json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	if got := object.OwnProperties(); string(got[2].NameJSON) != `"tail"` || string(got[2].Value) != "null" {
		t.Fatal("escaped equivalent key gained a new insertion slot")
	}
	if err := object.DeletePropertyJSON(json.RawMessage(`"\ufffd"`)); err != nil {
		t.Fatal(err)
	}
	if err := object.DeletePropertyJSON(json.RawMessage(`"\ud800"`)); err != nil {
		t.Fatal(err)
	}
	if err := object.SetPropertyJSON(json.RawMessage(`"\ud800"`), json.RawMessage(`false`)); err != nil {
		t.Fatal(err)
	}
	got := object.OwnProperties()
	if len(got) != 6 || string(got[5].NameJSON) != `"\ud800"` || string(got[5].Value) != "false" {
		t.Fatalf("exact UTF-16 delete/reinsert changed another key: %+v", got)
	}
}

func TestPropertyJSONRejectsBadKeyBeforeMutation(t *testing.T) {
	object := NewJSONObject(JSONField{Name: "keep", Value: json.RawMessage(`1`)})
	before := object.OwnProperties()
	for _, key := range []string{"", "null", "[]", "0", `"key" true`, `"unterminated`} {
		if err := object.SetPropertyJSON(json.RawMessage(key), json.RawMessage(`2`)); err == nil {
			t.Fatal("invalid property key acquired a slot", key)
		}
		if err := object.DeletePropertyJSON(json.RawMessage(key)); err == nil {
			t.Fatal("invalid property key deleted a slot", key)
		}
		if !reflect.DeepEqual(object.OwnProperties(), before) {
			t.Fatal("invalid key modified original ordered properties")
		}
	}
	var empty JSONObject
	if got := empty.OwnProperties(); got == nil || len(got) != 0 {
		t.Fatal("empty record snapshot should be an independent empty slice")
	}
	if err := empty.DeletePropertyJSON(json.RawMessage(`"absent"`)); err != nil {
		t.Fatal(err)
	}
}

func TestOwnPropertiesSnapshotsCanBeReadIndependently(t *testing.T) {
	object := NewJSONObject(JSONField{Name: "stable", Value: json.RawMessage(`"text"`)})
	var workers sync.WaitGroup
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			properties := object.OwnProperties()
			properties[0].NameJSON[1] = 'X'
			properties[0].Value[1] = 'X'
		}()
	}
	workers.Wait()
	if got := object.OwnProperties(); string(got[0].NameJSON) != `"stable"` || string(got[0].Value) != `"text"` {
		t.Fatal("parallel snapshots shared mutable name/value buffers")
	}
}
