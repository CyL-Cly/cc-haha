package sdkpermissions

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativeHookSettingsProofSurvivesPermissionStateReload(t *testing.T) {
	owner, _, gate := setup(t, "")
	var settings protocol.JSONObject
	_ = settings.UnmarshalJSON([]byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"actual native declaration"}]}]},"disableAllHooks":false,"future":true}`))
	if e := ValidateNativeWithBindings(&settings, false, false, false, false, false, false); e == nil {
		t.Fatal("unbound Hook consumer admitted")
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := Input{Profile: PublicPrintProfile, NativeProcess: true, NativeHookBindings: true, Platform: "darwin", Model: "actual", NonInteractive: true, BaseToolsCLI: []string{"Read"}, Mode: "default", Allowed: []string{"Read"}}
		state, e := Initialize(scope, owner, &settings, gate, input)
		if e != nil {
			return e
		}
		updated := settings.Clone()
		updated.Set("allowManagedHooksOnly", []byte("true"))
		if e = state.ReloadSettings(scope, &updated, map[string]RuleSet{"userSettings": {Allow: []string{"Read"}}}); e != nil {
			return e
		}
		actual, permission, e := state.Read(scope)
		if e != nil {
			return e
		}
		if actual != &updated || string(actual.Get("future")) != "true" || permission.String("mode") != "default" || string(permission.Get("alwaysAllowRules")) == "null" {
			t.Fatal("reload lost bound settings/mode/rules")
		}
		state.nativeHookBindings = false
		if e = state.ReloadSettings(scope, &updated, nil); e == nil {
			t.Fatal("reload dropped actual Hook proof guard")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
