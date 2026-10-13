package sdkhost

import "testing"

func TestInternalPermissionPersistenceRejectsOtherSettingsAndRawDataChanges(t *testing.T) {
	before := modelSettings(`{"model":"owned-model","permissions":{"allow":["Read"],"future":"\ud800"},"env":{"fixture":"kept"},"future":"\udc00"}`)
	for _, raw := range []string{
		`{"model":"owned-model","permissions":{"allow":["Read","Bash(printf:*)"],"future":"\ud800"},"env":{"fixture":"kept"},"future":"\udc00"}`,
		`{"model":"owned-model","permissions":{"allow":["Read"],"deny":["Write"],"ask":["Bash"],"additionalDirectories":["/owned"],"defaultMode":"plan","future":"\ud800"},"env":{"fixture":"kept"},"future":"\udc00"}`,
	} {
		if !sameSettingsOutsidePermissionPersistence(before, modelSettings(raw)) {
			t.Fatalf("declared permission edit refused %s", raw)
		}
	}
	for _, raw := range []string{
		`{"model":"changed","permissions":{"allow":["Read"],"future":"\ud800"},"env":{"fixture":"kept"},"future":"\udc00"}`,
		`{"model":"owned-model","permissions":{"allow":["Read"],"disableAutoMode":"disable","future":"\ud800"},"env":{"fixture":"kept"},"future":"\udc00"}`,
		`{"model":"owned-model","permissions":{"allow":["Read"],"future":"�"},"env":{"fixture":"kept"},"future":"\udc00"}`,
		`{"model":"owned-model","permissions":{"allow":["Read"],"future":"\ud800"},"env":{"fixture":"changed"},"future":"\udc00"}`,
		`{"model":"owned-model","permissions":{"allow":["Read"],"future":"\ud800"},"env":{"fixture":"kept"},"future":"�"}`,
	} {
		if sameSettingsOutsidePermissionPersistence(before, modelSettings(raw)) {
			t.Fatalf("another consumer or raw UTF16 edit admitted %s", raw)
		}
	}
}
