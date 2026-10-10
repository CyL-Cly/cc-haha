package protocol_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Dynamic admission makes the first unchanged-production regression compile:
// absence of shared nested access is a semantic failure, never a compiler kill.
func nestedObject(t *testing.T, value protocol.JSONObject, key string) protocol.JSONObject {
	t.Helper()
	access, ok := any(value).(interface {
		Object(string) (protocol.JSONObject, bool)
	})
	if !ok {
		t.Fatal("original JSON message has no shared nested object access")
	}
	next, ok := access.Object(key)
	if !ok {
		t.Fatalf("%q is not an original object", key)
	}
	return next
}
func TestNestedJSONCloneKeepsUnknownChildReference(t *testing.T) {
	var original protocol.JSONObject
	if e := original.UnmarshalJSON([]byte(`{"payload":{"\ud800":{"raw":9007199254740993},"2":"two","text":"before"},"top":"original"}`)); e != nil {
		t.Fatal(e)
	}
	clone := original.Clone()
	assigned := protocol.NewJSONObject()
	assigned.Assign(original)
	child := nestedObject(t, original, "payload")
	child.Set("text", json.RawMessage(`"later"`))
	clone.Set("top", json.RawMessage(`"copy"`))
	for _, m := range []protocol.JSONObject{original, clone, assigned} {
		b, e := m.MarshalJSON()
		if e != nil {
			t.Fatal(e)
		}
		var parsed map[string]json.RawMessage
		if e = json.Unmarshal(b, &parsed); e != nil {
			t.Fatal(e)
		}
		var payload map[string]json.RawMessage
		_ = json.Unmarshal(parsed["payload"], &payload)
		if string(payload["text"]) != `"later"` {
			t.Fatalf("shared child replacement invisible: %s", b)
		}
	}
	if string(original.Get("top")) != `"original"` {
		t.Fatal("clone replaced original own property")
	}
	snapshot := original.Get("payload")
	snapshot[1] = 'X'
	if string(child.Get("text")) != `"later"` {
		t.Fatal("raw snapshot buffer mutated actual child")
	}
}
func TestNestedJSONDecodeReplacementRetainsPriorObject(t *testing.T) {
	var root protocol.JSONObject
	_ = root.UnmarshalJSON([]byte(`{"message":{"content":"before"}}`))
	copy := root.Clone()
	child := nestedObject(t, root, "message")
	root.Set("message", json.RawMessage(`{"content":"replacement"}`))
	child.Set("content", json.RawMessage(`"retained prior child"`))
	if string(nestedObject(t, copy, "message").Get("content")) != `"retained prior child"` || string(nestedObject(t, root, "message").Get("content")) != `"replacement"` {
		t.Fatal("property assignment changed existing child identity")
	}
}

func TestNestedJSONFilteredArraySharesKeptObjectsButKeepsItsOwnLength(t *testing.T) {
	var root protocol.JSONObject
	_ = root.UnmarshalJSON([]byte(`{"content":[{"type":"text","text":"before","\ud800":{"unknown":9007199254740993}},{"type":"tool_use","name":"REPL"},{"type":"tool_result","tool_use_id":"unknown"}]}`))
	original, ok := root.Array("content")
	if !ok {
		t.Fatal("original array absent")
	}
	kept, e := original.Select([]int{0, 2})
	if e != nil {
		t.Fatal(e)
	}
	clone := root.Clone()
	clone.SetArray("content", kept)
	first, ok := original.Object(0)
	if !ok {
		t.Fatal("original block absent")
	}
	unknown, ok, e := first.ObjectPropertyJSON(json.RawMessage(`"\ud800"`))
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	unknown.Set("late", json.RawMessage(`true`))
	first.Set("text", json.RawMessage(`"before later"`))
	if e := original.Append(json.RawMessage(`{"type":"text","text":"new original"}`)); e != nil {
		t.Fatal(e)
	}
	copied, ok := clone.Array("content")
	if !ok || copied.Len() != 2 || original.Len() != 4 {
		t.Fatal("filter reused original array identity")
	}
	block, ok := copied.Object(0)
	if !ok || block.String("text") != "before later" {
		t.Fatal("filter detached kept object")
	}
	b, e := clone.MarshalJSON()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(b, []byte(`"\ud800":{"unknown":9007199254740993,"late":true}`)) {
		t.Fatal(string(b))
	}
	if e := copied.Set(0, json.RawMessage(`false`)); e != nil {
		t.Fatal(e)
	}
	if first.String("text") != "before later" {
		t.Fatal("array slot replacement mutated kept object")
	}
	before := root.Get("content")
	before[1] = 'X'
	if original.Len() != 4 {
		t.Fatal("raw array snapshot mutated original")
	}
}
func TestNestedJSONSerializationRejectsActualCyclesAndInvalidValues(t *testing.T) {
	root := protocol.NewJSONObject()
	root.SetObject("self", root)
	if _, e := root.MarshalJSON(); e == nil {
		t.Fatal("actual shared graph cycle serialized")
	}
	root.Delete("self")
	root.Set("bad", json.RawMessage(`{`))
	if _, e := root.MarshalJSON(); e == nil {
		t.Fatal("invalid raw field serialized")
	}
	var original protocol.JSONObject
	_ = original.UnmarshalJSON([]byte(`{"payload" : { "z" : 1e400 }, "items" : [null, true]}`))
	before := original.Get("payload")
	child, _ := original.Object("payload")
	_ = child
	after := original.Get("payload")
	if !bytes.Equal(before, after) {
		t.Fatal("read-only reference access rewrote original raw lexemes")
	}
	array, _ := original.Array("items")
	copy := array.Clone()
	if e := copy.Set(0, nil); e != nil {
		t.Fatal(e)
	}
	b, e := copy.MarshalJSON()
	if e != nil || string(b) != `[null,true]` {
		t.Fatal(string(b), e)
	}
	if _, e := array.Select([]int{-1}); e == nil {
		t.Fatal("invalid selection accepted")
	}
}
