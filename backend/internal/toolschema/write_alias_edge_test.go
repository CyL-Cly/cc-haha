package toolschema

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"reflect"
	"testing"
)

func TestWriteAliasPreprocessStrictEqualityAndObjectIdentity(t *testing.T) {
	d, e := NativeBuiltinDefinition("Write")
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		source string
		issues int
	}{{`{"file_path":1,"path":1.0,"content":"x"}`, 2}, {`{"file_path":null,"path":null,"content":"x"}`, 2}, {`{"file_path":{},"path":{},"content":"x"}`, 3}, {`{"file_path":"a","path":"b","content":"x"}`, 1}, {`{"file_path":"x","content":"a","file_text":"a","file_content":"a"}`, 0}}
	for _, c := range cases {
		t.Run(c.source, func(t *testing.T) {
			var input protocol.JSONObject
			if e = json.Unmarshal([]byte(c.source), &input); e != nil {
				t.Fatal(e)
			}
			_, e = ParseInput(d, input)
			if c.issues == 0 {
				if e != nil {
					t.Fatal(e)
				}
				return
			}
			var v *ValidationError
			if !errors.As(e, &v) {
				t.Fatal(e)
			}
			var issues []json.RawMessage
			if e = json.Unmarshal(v.Issues, &issues); e != nil || len(issues) != c.issues {
				t.Fatalf("actual issue count %s %v expected%d", v.Issues, e, c.issues)
			}
		})
	}
}
func TestWriteUnknownKeysAndValuesRetainJavaScriptCodeUnits(t *testing.T) {
	d, e := NativeBuiltinDefinition("Write")
	if e != nil {
		t.Fatal(e)
	}
	var input protocol.JSONObject
	if e = json.Unmarshal([]byte(`{"file_path":"x","content":"\ud800","bad\ud800":"x"}`), &input); e != nil {
		t.Fatal(e)
	}
	_, e = ParseInput(d, input)
	var v *ValidationError
	if !errors.As(e, &v) {
		t.Fatal(e)
	}
	var issues []protocol.JSONObject
	if e = json.Unmarshal(v.Issues, &issues); e != nil || len(issues) != 1 {
		t.Fatalf("issue %s %v", v.Issues, e)
	}
	var keys []json.RawMessage
	if e = json.Unmarshal(issues[0].Get("keys"), &keys); e != nil {
		t.Fatal(e)
	}
	units, e := protocol.DecodeJavaScriptJSONString(keys[0])
	if e != nil || !reflect.DeepEqual(units, []uint16{'b', 'a', 'd', 0xd800}) {
		t.Fatalf("keys %v %v", units, e)
	}
	message, e := protocol.DecodeJavaScriptJSONString(issues[0].Get("message"))
	if e != nil || message[len(message)-2] != 0xd800 {
		t.Fatalf("message units %v %v", message, e)
	}
	var canonical protocol.JSONObject
	if e = json.Unmarshal([]byte(`{"path":"x","file_content":"\ud800"}`), &canonical); e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseInput(d, canonical)
	if e != nil {
		t.Fatal(e)
	}
	units, e = protocol.DecodeJavaScriptJSONString(parsed.Get("content"))
	if e != nil || !reflect.DeepEqual(units, []uint16{0xd800}) {
		t.Fatalf("content %v %v", units, e)
	}
}
