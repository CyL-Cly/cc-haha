package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePersistedEffortAdmitsOnlyActualExternalInitialLevels(t *testing.T) {
	for _, value := range []string{`"low"`, `"medium"`, `"high"`} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "effortLevel", Value: []byte(value)})
		if err := ValidateNative(&settings); err != nil || string(settings.Get("effortLevel")) != value {
			t.Fatalf("original persisted effort %s: %v", value, err)
		}
		if err := ValidateNeutral(&settings); err == nil {
			t.Fatal("old finite guard broadened")
		}
	}
	for _, value := range []string{`"xhigh"`, `"max"`, `"invalid"`, `17`, `null`, `false`, `[]`, `{}`} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "effortLevel", Value: []byte(value)})
		if err := ValidateNative(&settings); err == nil {
			t.Fatalf("unconsumed raw persisted effort %s", value)
		}
	}
}
