package sdkpermissions

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestThemeSyntaxPreferenceRequiresSharedNativeStartupPresentationAndBoolean(t *testing.T) {
	bindings := make([]bool, 18)
	bindings[2], bindings[15] = true, true
	for _, value := range []string{"true", "false"} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "syntaxHighlightingDisabled", Value: []byte(value)})
		if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err != nil {
			t.Fatal(err)
		}
		for _, index := range []int{2, 15} {
			missing := append([]bool{}, bindings...)
			missing[index] = false
			if err := ValidateNativeWithBindings(&settings, false, false, missing...); err == nil {
				t.Fatal("missing real consumer admitted", index)
			}
		}
	}
	for _, value := range []string{`"false"`, `1`, `null`, `{}`} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "syntaxHighlightingDisabled", Value: []byte(value)})
		if err := ValidateNativeWithBindings(&settings, false, false, bindings...); err == nil {
			t.Fatal("invalid Sourceboolean", value)
		}
	}
}
