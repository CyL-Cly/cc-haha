package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestAutoMemoryDirectoryRequiresTypedInternalReadBindingAndString(t *testing.T) {
	for _, raw := range []string{`"~/trusted"`, `""`, `"../relative"`} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "autoMemoryDirectory", Value: []byte(raw)})
		if e := ValidateNativeWithBindings(&settings, false, false); e == nil {
			t.Fatal("unbound memory directory admitted")
		}
		if e := ValidateNativeWithBindings(&settings, false, false, false, false, false, false, false, false, false, false, false, false, true); e != nil {
			t.Fatal(raw, e)
		}
	}
	settings := protocol.NewJSONObject(protocol.JSONField{Name: "autoMemoryDirectory", Value: []byte("false")})
	if e := ValidateNativeWithBindings(&settings, false, false, false, false, false, false, false, false, false, false, false, false, true); e == nil {
		t.Fatal("invalid memory schema admitted")
	}
}
