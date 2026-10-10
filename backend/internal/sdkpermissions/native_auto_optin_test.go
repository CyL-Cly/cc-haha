package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeOrdinaryStartupAcceptsPersistedAutoOptInOnlyWithActualStartup(t *testing.T) {
	for _, value := range []string{"true", "false", `"true"`, "null", "0", "{}"} {
		var settings protocol.JSONObject
		_ = settings.UnmarshalJSON([]byte(`{"skipAutoPermissionPrompt":` + value + `}`))
		if e := ValidateNative(&settings); e == nil {
			t.Fatal("unbound startup accepted opt-in", value)
		}
		e := ValidateNativeWithBindings(&settings, false, false, false, false, true)
		if (e == nil) != (value == "true" || value == "false") {
			t.Fatalf("actual startup flag%s error%v", value, e)
		}
	}
}
