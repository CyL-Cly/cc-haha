package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestStatusLineFiniteSettingsRequireActualNativeStartupShellAndComponentBindings(t *testing.T) {
	settings := protocol.NewJSONObject(protocol.JSONField{Name: "statusLine", Value: []byte(`{"type":"command","command":"private-command","padding":2}`)})
	if err := ValidateNeutral(&settings); err == nil {
		t.Fatal("finite neutral Host admitted executable statusline")
	}
	if err := ValidateNative(&settings); err == nil {
		t.Fatal("native-looking JSON without actual consumers admitted statusline")
	}
	bindings := make([]bool, 19)
	bindings[2], bindings[4], bindings[18] = true, true, true
	for _, index := range []int{2, 4, 18} {
		missing := append([]bool(nil), bindings...)
		missing[index] = false
		if err := ValidateNativeWithBindings(&settings, false, false, missing...); err == nil {
			t.Fatal("missing actual startup/shell/component consumer", index)
		}
	}
	// The positive consumer is proven through actual MainPrint/physical shell
	// tests, rather than treating caller-supplied booleans as a native host.
}
