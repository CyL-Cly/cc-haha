package toolschema

import (
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeReadEditGrepBashInputCoercionAndStrictKeys(t *testing.T) {
	for _, tc := range []struct{ name, input, key, want string }{{"Read", `{"file_path":"x","offset":"2"}`, "offset", "2"}, {"Edit", `{"file_path":"x","old_string":"a","new_string":"b","replace_all":"false"}`, "replace_all", "false"}, {"Grep", `{"pattern":"x","-i":"true"}`, "-i", "true"}, {"Bash", `{"command":"pwd","timeout":"100"}`, "timeout", "100"}} {
		t.Run(tc.name, func(t *testing.T) {
			d, e := NativeBuiltinDefinition(tc.name)
			if e != nil {
				t.Fatal(e)
			}
			var in protocol.JSONObject
			_ = json.Unmarshal([]byte(tc.input), &in)
			out, e := ParseInput(d, in)
			if e != nil || string(out.Get(tc.key)) != tc.want {
				t.Fatalf("parse %s %v", raw(out), e)
			}
			in.Set("unexpected", []byte("true"))
			if _, e = ParseInput(d, in); e == nil {
				t.Fatal("unknown key admitted")
			}
		})
	}
}
