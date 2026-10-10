package sdkpermissions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePermissionProvenanceAndOwnerModeTransition(t *testing.T) {
	owner, settings, gate := setup(t, "")
	settings.Set("permissions", []byte(`{"allow":["Read"],"deny":["Read(secret)"]}`))
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, e := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "windows", Model: "arbitrary-real-model", BaseToolsCLI: []string{"Read"}, Mode: "default", AllowBypass: true, PermissionRules: map[string]RuleSet{"policySettings": {Deny: []string{"Read(secret)"}}, "projectSettings": {Allow: []string{"Read"}}}})
		if e != nil {
			return e
		}
		_, before, e := state.Read(scope)
		if e != nil {
			return e
		}
		var deny, allow map[string][]string
		_ = json.Unmarshal(before.Get("alwaysDenyRules"), &deny)
		_ = json.Unmarshal(before.Get("alwaysAllowRules"), &allow)
		if !reflect.DeepEqual(deny["policySettings"], []string{"Read(secret)"}) || !reflect.DeepEqual(allow["projectSettings"], []string{"Read"}) || len(allow["userSettings"]) != 0 {
			return fmt.Errorf("settings sources changed %s %s", before.Get("alwaysDenyRules"), before.Get("alwaysAllowRules"))
		}
		if e = state.SetMode(scope, "plan"); e != nil {
			return e
		}
		_, after, e := state.Read(scope)
		if e != nil {
			return e
		}
		if after.String("mode") != "plan" {
			return fmt.Errorf("mode %s", after.String("mode"))
		}
		before.Set("mode", quote("plan"))
		before.Set("prePlanMode", quote("default"))
		b, _ := before.MarshalJSON()
		a, _ := after.MarshalJSON()
		if string(b) != string(a) {
			return fmt.Errorf("mode transition changed permission rules %s %s", b, a)
		}
		return state.SetMode(scope, "bypassPermissions")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativePermissionProfileRejectsUnconsumedPolicyAndMissingProvenance(t *testing.T) {
	for _, settingsJSON := range []string{`{"permissions":{"additionalDirectories":["/private-workspace"]}}`, `{"permissions":{"onlyManagedPermissionRules":true}}`, `{"onlyManagedPermissionRules":true}`, `{"allowManagedPermissionRulesOnly":true}`, `{"permissions":{"defaultMode":"plan"}}`, `{"permissions":{"disableBypassPermissionsMode":"disable"}}`} {
		var settings protocol.JSONObject
		if err := settings.UnmarshalJSON([]byte(settingsJSON)); err != nil {
			t.Fatal(err)
		}
		if err := ValidateNative(&settings); err == nil {
			t.Fatalf("unconsumed policy admitted: %s", settingsJSON)
		}
	}
	owner, settings, gate := setup(t, "")
	settings.Set("permissions", []byte(`{"allow":["Read"]}`))
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "linux", Model: "model", BaseToolsCLI: []string{"Read"}, Mode: "default"})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("merged policy accepted without source map: %v", err)
	}
}

func TestNativePermissionModeCannotEnableUnavailableBypass(t *testing.T) {
	owner, settings, gate := setup(t, "")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, e := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "model", BaseToolsCLI: []string{"Read"}, Mode: "default"})
		if e != nil {
			return e
		}
		if e = state.SetMode(scope, "bypassPermissions"); e == nil {
			return fmt.Errorf("unavailable bypass enabled")
		}
		_, p, e := state.Read(scope)
		if e == nil && p.String("mode") != "default" {
			return fmt.Errorf("rejected transition changed mode")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeThinkingSettingsPreserveBooleanAndRejectUnconsumedValues(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "alwaysThinkingEnabled", Value: []byte(value)})
		if e := ValidateNative(&settings); e != nil || string(settings.Get("alwaysThinkingEnabled")) != value {
			t.Fatalf("native thinking %s: %v", value, e)
		}
		if e := ValidateNeutral(&settings); e == nil {
			t.Fatal("finite settings guard broadened")
		}
	}
	for _, value := range []string{"null", `"false"`, "0"} {
		settings := protocol.NewJSONObject(protocol.JSONField{Name: "alwaysThinkingEnabled", Value: []byte(value)})
		if e := ValidateNative(&settings); e == nil {
			t.Fatalf("invalid native thinking %s", value)
		}
	}
}
