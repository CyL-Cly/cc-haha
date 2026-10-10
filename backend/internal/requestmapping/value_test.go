package requestmapping

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValueWirePreservesJSKeyOrderUTF16AndDuplicateKeys(t *testing.T) {
	source := ` {"b":1,"a":2,"10":true,"2":null,"\ud800":"high","\udfff":"low","\u0061":3,"nested":{"z":1,"a":0},"future":[false,null,"\ud800"],"high":9007199254740993,"overflow":1e400} `
	input := []byte(source)
	parsed, err := ParseValue(input)
	if err != nil {
		t.Fatal(err)
	}
	for index := range input {
		input[index] = 'x'
	}
	expected := `{"2":null,"10":true,"b":1,"a":3,"\ud800":"high","\udfff":"low","nested":{"z":1,"a":0},"future":[false,null,"\ud800"],"high":9007199254740992,"overflow":null}`
	if actual := wire(t, parsed); actual != expected {
		t.Fatalf("wire differs from JSON.parse→JSON.stringify: %s", actual)
	}
	if actual := wire(t, parsed.Property("future").Element(2)); actual != `"\ud800"` {
		t.Fatal("UTF-16 raw primitive lost")
	}
	// SetProperty(nil) is the only undefined operation needed by the seam.
	if !parsed.SetProperty("unset", nil) || strings.Contains(wire(t, parsed), "unset") {
		t.Fatal("undefined object field must be omitted")
	}
	for _, raw := range []string{"", `[`, `{"bad":}`, `null trailing`} {
		if _, err := ParseValue(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid adapter JSON accepted")
		}
	}
}

func TestValueAccessAndMutationGuards(t *testing.T) {
	var missing *Value
	if missing.Property("x") != nil || missing.Element(0) != nil {
		t.Fatal("missing accessor should stay undefined")
	}
	if actual, err := missing.MarshalJSON(); err != nil || string(actual) != "null" {
		t.Fatal("undefined array item serializes as null")
	}
	if _, array := missing.ArrayLength(); array {
		t.Fatal("undefined is not an array")
	}
	if missing.SetProperty("x", nil) || missing.SetElement(0, nil) || missing.Append(nil) {
		t.Fatal("missing mutation must fail")
	}
	array := value(t, `[1]`)
	if array.Element(-1) != nil || array.Element(1) != nil || array.SetElement(-1, nil) || array.SetElement(1, nil) {
		t.Fatal("bounds guard changed")
	}
	if !array.SetElement(0, nil) || !array.Append(value(t, `true`)) || wire(t, array) != `[null,true]` {
		t.Fatal("array slot/append semantics changed")
	}
	object := value(t, `{}`)
	if object.SetElement(0, nil) || object.Append(nil) {
		t.Fatal("object is not an array")
	}
	primitive := value(t, `false`)
	if primitive.SetProperty("x", nil) || primitive.SetElement(0, nil) || primitive.Append(nil) {
		t.Fatal("mutators must not coerce primitives")
	}
	if _, array := object.ArrayLength(); array {
		t.Fatal("object is not an array")
	}
	if !object.SetProperty("field", value(t, `true`)) || !object.SetProperty("field", value(t, `false`)) || wire(t, object) != `{"field":false}` {
		t.Fatal("object overwrite should retain one field")
	}
	if encoded, err := json.Marshal(&MessageParam{Role: "user", Content: array}); err != nil || string(encoded) != `{"role":"user","content":[null,true]}` {
		t.Fatal("MessageParam must support encoding/json")
	}
}
