package pluginschema

import (
	"encoding/json"
	"testing"
)

func TestMissingRegistryAndManifestPropertiesAreValidationErrors(t *testing.T) {
	schemas, err := Declarations()
	if err != nil {
		t.Fatal(err)
	}
	for name, schema := range map[string]*Node{"installed": schemas.Installed, "manifest": schemas.Manifest, "marketplace": schemas.Marketplace} {
		t.Run(name, func(t *testing.T) {
			for _, value := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`null`)} {
				_, issues := Validate(schema, value)
				if len(issues) == 0 {
					t.Fatalf("missing required fields accepted: %s", value)
				}
			}
		})
	}
}
