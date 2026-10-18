package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeTerminalPreferencesRequireActualSharedStartupAndSpinnerConsumers(t *testing.T) {
	bindings := make([]bool, 18)
	bindings[2] = true
	bindings[15] = true
	for _, key := range []string{"spinnerTipsEnabled", "prefersReducedMotion"} {
		for _, value := range []string{"true", "false"} {
			settings := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(value)})
			if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err != nil {
				t.Fatal(key, err)
			}
			for _, index := range []int{2, 15} {
				copy := append([]bool{}, bindings...)
				copy[index] = false
				if err := ValidateNativeWithBindings(&settings, false, false, copy...); err == nil {
					t.Fatal("missing actual consumer admitted", key, index)
				}
			}
		}
		settings := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`"false"`)})
		if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err == nil {
			t.Fatal("invalid bool", key)
		}
	}
}
