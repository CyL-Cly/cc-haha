package toolschema

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWriteOriginalSchemaOnly(t *testing.T) {
	d, e := NativeBuiltinDefinition("Write")
	if e != nil {
		t.Fatal(e)
	}
	got, e := OriginalInputJSONSchema(d)
	if e != nil {
		t.Fatal(e)
	}
	want, e := compileSchema(*d.InputSchema, true)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %s want %s", got, want)
	}
	var o map[string]json.RawMessage
	if e = json.Unmarshal(got, &o); e != nil {
		t.Fatal(e)
	}
	if o["$schema"] == nil {
		t.Fatal("original root declaration lost")
	}
}
func TestWriteOriginalLiteralNullishAndMissing(t *testing.T) {
	literal := json.RawMessage(`{"type":"object","strict":false,"additionalProperties":true,"custom":"keep"}`)
	d := Definition{InputJSONSchema: literal}
	got, e := OriginalInputJSONSchema(d)
	if e != nil || !bytes.Equal(got, literal) {
		t.Fatalf("literal %s %v", got, e)
	}
	for _, nullish := range []json.RawMessage{nil, json.RawMessage(" null ")} {
		d.InputJSONSchema = nullish
		d.InputSchema = &Schema{Kind: "object"}
		got, e = OriginalInputJSONSchema(d)
		if e != nil || !json.Valid(got) {
			t.Fatalf("nullish %s %v", got, e)
		}
	}
	d.InputSchema = nil
	d.InputJSONSchema = nil
	if _, e = OriginalInputJSONSchema(d); e == nil {
		t.Fatal("missing schema allowed")
	}
}
func TestWriteOriginalSchemaNoSessionEffects(t *testing.T) {
	d, e := NativeBuiltinDefinition("Write")
	if e != nil {
		t.Fatal(e)
	}
	// Poisoned prompt/description prove the schema-only path does not evaluate them.
	d.Prompt = json.RawMessage("null")
	d.Description = json.RawMessage("null")
	session := NewSession()
	before := session.CacheSize()
	calls := session.PromptCalls()
	for i := 0; i < 2; i++ {
		if _, e = OriginalInputJSONSchema(d); e != nil {
			t.Fatal(e)
		}
	}
	if session.CacheSize() != before || len(calls) != len(session.PromptCalls()) {
		t.Fatal("schema-only call used request session")
	}
}
