package sdkhost

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePluginOptionsCacheDeltaOnlyAllowsCapturedPluginOrServerDomain(t *testing.T) {
	base := `{"enabledPlugins":{"sample@mkt":true},"pluginConfigs":{"sample@mkt":{"options":{"region":"North","oldSensitive":"legacy"},"mcpServers":{"chat":{"region":"North","oldSensitive":"legacy"},"other":{"keep":true}},"future":"keep"},"other@mkt":{"options":{"keep":true}}},"permissions":{"allow":["Read"]},"unknown":{"keep":true}}`
	parse := func(text string) protocol.JSONObject {
		t.Helper()
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, tc := range []struct {
		name, id, server string
		path             []string
		value            string
		allowed          bool
	}{
		{"same-settings-secret-only", "sample@mkt", "", nil, "", true},
		{"top-region-change-and-sensitive-scrub", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt", "options"}, `{"region":"South"}`, true},
		{"top-empty-object", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt", "options"}, `{}`, true},
		{"channel-change-and-sensitive-scrub", "sample@mkt", "chat", []string{"pluginConfigs", "sample@mkt", "mcpServers", "chat"}, `{"region":"South"}`, true},
		{"other-plugin", "sample@mkt", "", []string{"pluginConfigs", "other@mkt", "options"}, `{"keep":false}`, false},
		{"other-channel", "sample@mkt", "chat", []string{"pluginConfigs", "sample@mkt", "mcpServers", "other"}, `{"keep":false}`, false},
		{"top-cannot-change-channel", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt", "mcpServers", "chat"}, `{}`, false},
		{"channel-cannot-change-top", "sample@mkt", "chat", []string{"pluginConfigs", "sample@mkt", "options"}, `{}`, false},
		{"enabled-is-another-producer", "sample@mkt", "", []string{"enabledPlugins", "sample@mkt"}, `false`, false},
		{"marketplace-is-another-producer", "sample@mkt", "", []string{"extraKnownMarketplaces"}, `{}`, false},
		{"permission-context", "sample@mkt", "", []string{"permissions", "allow"}, `["Bash"]`, false},
		{"unknown-setting", "sample@mkt", "", []string{"unknown", "keep"}, `false`, false},
		{"unrelated-plugin-field", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt", "future"}, `"changed"`, false},
		{"whole-plugin-deletion", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt"}, "", false},
		{"options-null", "sample@mkt", "", []string{"pluginConfigs", "sample@mkt", "options"}, `null`, false},
		{"empty-id", "", "", nil, "", false},
		{"wrong-captured-id", "other@mkt", "", []string{"pluginConfigs", "sample@mkt", "options"}, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := parse(base), parse(base)
			var set func(*protocol.JSONObject, []string)
			set = func(object *protocol.JSONObject, path []string) {
				if len(path) == 0 {
					return
				}
				if len(path) == 1 {
					if tc.value == "" {
						object.Delete(path[0])
					} else {
						object.Set(path[0], []byte(tc.value))
					}
					return
				}
				child, _ := object.Object(path[0])
				if !child.IsObject() {
					child = protocol.NewJSONObject()
				}
				set(&child, path[1:])
				object.Set(path[0], raw(child))
			}
			set(&after, tc.path)
			if got := sameSettingsOutsidePluginOptionsProducer(&before, &after, tc.id, tc.server); got != tc.allowed {
				t.Fatal("options producer accepted wrong settings delta", got, tc.allowed)
			}
		})
	}
	for _, server := range []string{"", "chat"} {
		t.Run("new-domain-"+server, func(t *testing.T) {
			before := parse(`{"unknown":{"keep":true}}`)
			after := parse(`{"unknown":{"keep":true},"pluginConfigs":{"sample@mkt":{"options":{"region":"North"}}}}`)
			if server != "" {
				after = parse(`{"unknown":{"keep":true},"pluginConfigs":{"sample@mkt":{"mcpServers":{"chat":{"region":"North"}}}}}`)
			}
			if !sameSettingsOutsidePluginOptionsProducer(&before, &after, "sample@mkt", server) {
				t.Fatal("real first options save cannot create its own domain")
			}
		})
	}
}
