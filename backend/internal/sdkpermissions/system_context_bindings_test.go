package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeGitInstructionsRequireActualSystemContextBinding(t *testing.T) {
	var settings protocol.JSONObject
	if err := settings.UnmarshalJSON([]byte(`{"includeGitInstructions":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNativeWithBindings(&settings, false, false, false, false, false, false, false); err == nil {
		t.Fatal("unbound settings admitted")
	}
	if err := ValidateNativeWithBindings(&settings, false, false, false, false, false, false, false, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNeutral(&settings); err == nil {
		t.Fatal("finite original profile widened")
	}
}
