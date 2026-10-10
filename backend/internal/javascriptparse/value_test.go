package javascriptparse

import (
	"encoding/json"
	"github.com/dop251/goja"
	"math"
	"testing"
)

func property(v *Value, key string) *Value {
	for _, p := range v.Properties {
		if stringUnitsEqual(p.Key, text(key)) {
			return p.Value
		}
	}
	return nil
}
func stringUnitsEqual(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func TestTypedLanguageValuesRetainPrimitiveAndOwnIdentity(t *testing.T) {
	vm := goja.New()
	raw, err := vm.RunString(`var shared={x:1};({missing:void 0,nil:null,empty:'',lone:'\ud800',bool:false,truth:true,neg:-0,nan:NaN,inf:Infinity,ninf:-Infinity,big:123456789012345678901n,regexp:/a+/gi,a:shared,b:shared,holes:[,void 0,null]})`)
	if err != nil {
		t.Fatal(err)
	}
	v := newEncoder().encode(raw)
	want := map[string]string{"missing": "undefined", "nil": "null", "empty": "string", "lone": "string", "bool": "boolean", "truth": "boolean", "neg": "number", "nan": "number", "inf": "number", "ninf": "number", "big": "bigint", "regexp": "regexp", "a": "object", "b": "reference", "holes": "array"}
	for key, kind := range want {
		if actual := property(v, key); actual == nil || actual.Kind != kind {
			t.Fatalf("%s: %+v", key, actual)
		}
	}
	if property(v, "absent") != nil || len(property(v, "lone").Units) != 1 || property(v, "lone").Units[0] != 0xd800 || property(v, "neg").Text != "-0" || property(v, "nan").Text != "NaN" || property(v, "inf").Text != "Infinity" || property(v, "ninf").Text != "-Infinity" || property(v, "big").Text != "123456789012345678901" {
		t.Fatalf("typed data lost: %+v", v)
	}
	if *property(v, "bool").Bool || !*property(v, "truth").Bool || property(v, "b").ID != property(v, "a").ID {
		t.Fatal("identity/bool lost")
	}
	holes := property(v, "holes")
	if property(holes, "0") != nil || property(holes, "1").Kind != "undefined" || property(holes, "2").Kind != "null" || property(holes, "length").Text != "3" {
		t.Fatal("own absence lost")
	}
	if encoded, err := json.Marshal(v); err != nil || len(encoded) == 0 {
		t.Fatal(err)
	}
}
func TestActualRuntimeCycleAndScalarCoercion(t *testing.T) {
	vm := goja.New()
	raw, err := vm.RunString(`var x={};x.self=x;x`)
	if err != nil {
		t.Fatal(err)
	}
	v := newEncoder().encode(raw)
	if property(v, "self").Kind != "reference" || property(v, "self").ID != v.ID {
		t.Fatal("cycle identity lost")
	}
	if newEncoder().encode(nil).Kind != "undefined" || newEncoder().encode(goja.Null()).Kind != "null" || len(stringUnits(goja.Undefined())) != 0 || !stringUnitsEqual(stringUnits(vm.ToValue(23)), text("23")) {
		t.Fatal("scalar coercion mismatch")
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1)} {
		if newEncoder().encode(vm.ToValue(f)).Kind != "number" {
			t.Fatal("number kind")
		}
	}
	funcValue, err := vm.RunString(`function f(){};f`)
	if err != nil {
		t.Fatal(err)
	}
	if v := newEncoder().encode(funcValue); v.Kind != "object" || v.Class != "Function" {
		t.Fatal("function own record")
	}
	symbol, err := vm.RunString(`Symbol('unregistered')`)
	if err != nil {
		t.Fatal(err)
	}
	if v := newEncoder().encode(symbol); v.Kind != "unsupported" {
		t.Fatal("unsupported primitive admitted")
	}
}
