package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestJavaScriptStringJSONForSessionDeltas(t *testing.T) {
	for _, fixture := range []struct{ input, want string }{
		{"", `"undefined"`},
		{" null ", `"null"`},
		{"true", `"true"`},
		{"-0", `"0"`},
		{"1e400", `"Infinity"`},
		{`"\ud800\udfff\ud800"`, `"𐏿\ud800"`},
		{`["\ud800",null,{},["x",false]]`, `"\ud800,,[object Object],x,false"`},
	} {
		got, err := JavaScriptStringJSON(json.RawMessage(fixture.input))
		if err != nil || string(got) != fixture.want {
			t.Errorf("String(%s) = %s, %v; want %s", fixture.input, got, err, fixture.want)
		}
	}
	for _, invalid := range []string{" ", "{", "null true"} {
		if got, err := JavaScriptStringJSON(json.RawMessage(invalid)); err == nil || got != nil {
			t.Errorf("malformed session input acquired text: %s, %v", got, err)
		}
	}
}

func TestSessionToolInputNormalizationRetainsUTF16AndFailureMarker(t *testing.T) {
	valid := []uint16{'{', '"', 'x', '"', ':', '"', 0xd800, '"', '}'}
	before := append([]uint16(nil), valid...)
	if got := NormalizeToolArgumentUTF16(valid, true); string(got) != `{"x":"\ud800"}` || !reflect.DeepEqual(valid, before) {
		t.Fatalf("session parsed input changed: %s, %x", got, valid)
	}
	invalid := []uint16{'{', 0xdfff}
	if got := NormalizeToolArgumentUTF16(invalid, true); string(got) != `{"__unparsedToolInput":{"raw":"{\udfff","len":2}}` {
		t.Fatalf("session malformed local tool bypassed validation: %s", got)
	}
	if got := NormalizeToolArgumentUTF16(invalid, false); string(got) != `{}` {
		t.Fatalf("server tool malformed input changed: %s", got)
	}
}
