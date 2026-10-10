package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestAPIKeyHelperRequiresItsActualNativeBindings(t *testing.T) {
	settings := protocol.NewJSONObject(protocol.JSONField{Name: "apiKeyHelper", Value: []byte(`"private actual command"`)})
	if err := ValidateNativeWithBindings(&settings, false, false); err == nil {
		t.Fatal("unbound helper admitted")
	}
	if err := ValidateNativeWithBindings(&settings, false, false, false, false, false, false, false, false, false, true); err != nil {
		t.Fatal(err)
	}
}
