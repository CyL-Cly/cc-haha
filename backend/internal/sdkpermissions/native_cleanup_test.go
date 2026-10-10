package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeCleanupConsumerAcceptsParsedDaysAndRetainsFiniteBoundary(t *testing.T) {
	for _, value := range []string{"0", "7", "15", "30", "365", "9007199254740991"} {
		var settings protocol.JSONObject
		if err := settings.UnmarshalJSON([]byte(`{"cleanupPeriodDays":` + value + `}`)); err != nil {
			t.Fatal(err)
		}
		if err := ValidateNative(&settings); err != nil {
			t.Fatalf("valid retention days %s: %v", value, err)
		}
		if err := ValidateNeutral(&settings); err == nil {
			t.Fatal("finite settings consumer widened")
		}
	}
	for _, value := range []string{"null", "true", `"30"`, "-1", "1.5", "9007199254740992", "1e309"} {
		var settings protocol.JSONObject
		if err := settings.UnmarshalJSON([]byte(`{"cleanupPeriodDays":` + value + `}`)); err != nil {
			t.Fatal(err)
		}
		if err := ValidateNative(&settings); err == nil {
			t.Fatalf("invalid retention days %s", value)
		}
	}
}
