package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
)

func TestSourceRetiredAttributionNeedsNoActiveSDKProducer(t *testing.T) {
	for _, content := range []string{`{"attribution":{}}`, `{"attribution":{"commit":"ignored","pr":"ignored"},"includeCoAuthoredBy":false}`, `{"includeCoAuthoredBy":true}`} {
		parsed, e := clisettings.ValidateNativeJSONContent(content, "fixture")
		if e != nil || parsed.Settings == nil {
			t.Fatal("native schema fixture", e)
		}
		if e = ValidateNativeWithBindings(parsed.Settings, false, false); e != nil {
			t.Fatal("Source retired fields acquired an active consumer", e)
		}
		if e = ValidateNeutral(parsed.Settings); e == nil {
			t.Fatal("finite neutral domain widened")
		}
	}
}
