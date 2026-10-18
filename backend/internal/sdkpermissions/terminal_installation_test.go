package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeInstallationSettingsRequireSharedStartupSchema(t *testing.T) {
	for key, values := range map[string][]string{"autoUpdatesChannel": {`"latest"`, `"stable"`}, "minimumVersion": {`"2.3.4"`, `"not-semver-source-accepts-string"`}} {
		for _, value := range values {
			settings := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(value)})
			bindings := make([]bool, 18)
			bindings[2] = true
			if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err != nil {
				t.Fatal(key, err)
			}
			bindings[2] = false
			if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err == nil {
				t.Fatal("unbound startup admitted", key)
			}
		}
	}
	for _, pair := range []struct{ key, value string }{{"autoUpdatesChannel", `"unpublished"`}, {"autoUpdatesChannel", `true`}, {"minimumVersion", `false`}} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: pair.key, Value: []byte(pair.value)})
		bindings := make([]bool, 18)
		bindings[2] = true
		if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err == nil {
			t.Fatal("invalid original schema admitted", pair)
		}
	}
}
