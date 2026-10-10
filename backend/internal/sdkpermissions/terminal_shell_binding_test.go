package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeTerminalShellSettingRetainsRealStartupBindingGuard(t *testing.T) {
	for _, shell := range []string{"bash", "powershell"} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "defaultShell", Value: quote(shell)})
		if e := ValidateNativeWithBindings(&settings, false, false, false, false, false); e == nil {
			t.Fatal("unbound terminal setting admitted", shell)
		}
		if e := ValidateNativeWithBindings(&settings, false, false, false, false, true); e != nil {
			t.Fatal("actual startup settings binding rejected", shell, e)
		}
	}
	for _, value := range []string{`null`, `true`, `"other"`} {
		var settings protocol.JSONObject
		_ = settings.UnmarshalJSON([]byte(`{"defaultShell":` + value + `}`))
		if e := ValidateNativeWithBindings(&settings, false, false, false, false, true); e == nil {
			t.Fatal("invalid shell bypassed schema proof", value)
		}
	}
}
