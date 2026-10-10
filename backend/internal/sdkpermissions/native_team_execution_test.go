package sdkpermissions

import (
	"context"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeTeamExecutionBindingSurvivesReadReloadAndLeavesFiniteGuard(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var settings protocol.JSONObject
	if err := settings.UnmarshalJSON([]byte(`{"agentTeamsEnabled":true,"future":{"keep":"\ud800"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNativeWithBindings(&settings, false, false); err == nil {
		t.Fatal("ordinary native settings admitted enabled teams")
	}
	if err := ValidateNeutral(&settings); err == nil {
		t.Fatal("finite guard widened")
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state := &State{owner: owner, settings: &settings, context: protocol.NewJSONObject(protocol.JSONField{Name: "alwaysAllowRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysDenyRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysAskRules", Value: []byte(`{}`)}), nativeProcess: true, nativeTeamExecutionBindings: true}
		if _, _, err := state.Read(scope); err != nil {
			return err
		}
		reloaded := settings.Clone()
		if err := state.ReloadSettings(scope, &reloaded, nil); err != nil {
			return err
		}
		if _, _, err := state.Read(scope); err != nil {
			return err
		}
		state.nativeTeamExecutionBindings = false
		if _, _, err := state.Read(scope); err == nil {
			t.Fatal("missing binding retained enabled admission")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `1`, `"true"`, `[]`, `{}`} {
		object := protocol.NewJSONObject(protocol.JSONField{Name: "agentTeamsEnabled", Value: []byte(value)})
		if err := ValidateNativeWithBindings(&object, false, false, false, false, false, false, false, false, true); err == nil {
			t.Fatalf("bad schema admitted: %s", value)
		}
	}
}
