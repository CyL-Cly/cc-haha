package sdkhost

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// A finite host cannot assert empty registry latches while its real Environment
// carries a second unexamined set of flags. This is initialization provenance,
// not a wildcard setting or environment allowlist.
func validateNeutralEnvironment(environment *clistartupenv.Environment, original map[string]string) error {
	if environment == nil {
		return cap("neutral original environment")
	}
	expected := protocol.NewJSONObject()
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR"} {
		value := environment.Get(key)
		if _, e := protocol.DecodeJavaScriptJSONString(value); e != nil {
			return cap("neutral original filesystem environment")
		}
		expected.Set(key, value)
	}
	for key, value := range original {
		raw, _ := json.Marshal(value)
		expected.Set(key, raw)
	}
	snapshot := environment.Snapshot()
	if len(snapshot.OwnProperties()) != len(expected.OwnProperties()) {
		return cap("unresolved neutral registry environment")
	}
	for _, property := range snapshot.OwnProperties() {
		found := false
		for _, wanted := range expected.OwnProperties() {
			if bytes.Equal(property.NameJSON, wanted.NameJSON) && bytes.Equal(property.Value, wanted.Value) {
				found = true
				break
			}
		}
		if !found {
			return cap("unresolved neutral registry environment")
		}
	}
	return nil
}
