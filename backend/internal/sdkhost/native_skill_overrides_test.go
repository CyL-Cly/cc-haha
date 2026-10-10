package sdkhost

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSkillPermissionOverlayRetainsRawIdentityAndPersistentContext(t *testing.T) {
	var actual protocol.JSONObject
	if err := actual.UnmarshalJSON([]byte(`{"mode":"default","alwaysAllowRules":{"session":["Read"],"command":["Bash","\ud800","Task"]},"alwaysDenyRules":{"policySettings":["Write"]},"additionalWorkingDirectories":{"map":[]},"unknown":{"keep":true}}`)); err != nil {
		t.Fatal(err)
	}
	var overrides protocol.JSONObject
	if err := overrides.UnmarshalJSON([]byte(`{"commandRules":["\u0042ash","\ud800","\udfff","Task","Agent"],"model":"skill-model","effort":0}`)); err != nil {
		t.Fatal(err)
	}
	h := &Host{}
	call := &builtinwrite.CallContext{MainLoopModel: "original-model", SkillOverrides: &overrides}
	if err := h.captureSkillCallOverrides(call); err != nil {
		t.Fatal(err)
	}
	if call.MainLoopModel != "skill-model" {
		t.Fatal("modifier model not carried to following tools", call.MainLoopModel)
	}
	got, err := h.overlaySkillPermission(actual)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"session":["Read"],"command":["Bash","\ud800","Task","\udfff","Agent"]}`
	if string(got.Get("alwaysAllowRules")) != want {
		t.Fatal("Source Set insertion/UTF-16 identity changed", string(got.Get("alwaysAllowRules")))
	}
	for _, key := range []string{"mode", "alwaysDenyRules", "additionalWorkingDirectories", "unknown"} {
		if string(got.Get(key)) != string(actual.Get(key)) {
			t.Fatal("unrelated permission provenance changed", key)
		}
	}
	if string(actual.Get("alwaysAllowRules")) != `{"session":["Read"],"command":["Bash","\ud800","Task"]}` {
		t.Fatal("ephemeral modifier mutated actual controller context")
	}
	// The top-level override is an ASK snapshot, not an alias to another call.
	overrides.Set("model", quote("later-alias-change"))
	model, err := h.skillMainModel("fallback")
	if err != nil || model != "skill-model" {
		t.Fatal("snapshot model aliased", model, err)
	}
	if string(h.askSkillOverrides.Get("effort")) != "0" {
		t.Fatal("zero effort presence lost")
	}
	if err := h.captureSkillCallOverrides(&builtinwrite.CallContext{MainLoopModel: "original-model"}); err != nil {
		t.Fatal(err)
	}
	if model, err := h.skillMainModel("original-model"); err != nil || model != "original-model" || h.askSkillOverrides != nil {
		t.Fatal("next unmodified call retained old skill overrides", model, err)
	}
	if err := h.captureSkillCallOverrides(nil); err == nil {
		t.Fatal("foreign modifier context admitted")
	}
}
