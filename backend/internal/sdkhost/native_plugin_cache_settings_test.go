package sdkhost

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Positive public installation/uninstallation proofs execute the real store
// producer. These counterexamples bound its certificate to one captured key.
func TestNativePluginUninstallCacheDeltaOnlyDeletesCapturedOwnOptions(t *testing.T) {
	before := `{"enabledPlugins":{"sample@mkt":true},"pluginConfigs":{"sample@mkt":{"options":{"keepSecretPresence":true}},"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`
	for _, tc := range []struct {
		name, after, id string
		allowed         bool
	}{
		{"target-options-only", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", true},
		{"same-settings-no-last-scope", before, "sample@mkt", true},
		{"ordinary-ticket-cannot-delete-options", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "", false},
		{"wrong-captured-target", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "other@mkt", false},
		{"other-options-mutated", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"changed"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", false},
		{"all-options-removed", `{"enabledPlugins":{},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", false},
		{"target-options-replaced", `{"enabledPlugins":{},"pluginConfigs":{"sample@mkt":{"options":{}},"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", false},
		{"target-options-null", `{"enabledPlugins":{},"pluginConfigs":{"sample@mkt":null,"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", false},
		{"permission-changed", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"yes"}}},"permissions":{"allow":["Bash"]},"unknown":{"keep":true}}`, "sample@mkt", false},
		{"new-options-key", `{"enabledPlugins":{},"pluginConfigs":{"other@mkt":{"options":{"retained":"yes"}},"new@mkt":{}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`, "sample@mkt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var old, next protocol.JSONObject
			if err := old.UnmarshalJSON([]byte(before)); err != nil {
				t.Fatal(err)
			}
			if err := next.UnmarshalJSON([]byte(tc.after)); err != nil {
				t.Fatal(err)
			}
			if allowed := sameSettingsOutsidePluginCacheProducer(&old, &next, tc.id); allowed != tc.allowed {
				t.Fatal("producer delta admitted wrong settings", allowed, tc.allowed)
			}
		})
	}
}
