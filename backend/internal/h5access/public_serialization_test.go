package h5access

import (
	"encoding/json"
	"testing"
)

// This public projection follows the source's toPublicSettings literal, whose
// ordering is distinct from storedSettings and is visible in HTTP JSON bytes.
func TestPublicSettingsRetainsHTTPPropertyOrder(t *testing.T) {
	raw, err := json.Marshal(PublicSettings{TokenPreview: json.RawMessage(`null`), AllowedOrigins: []string{}})
	want := `{"enabled":false,"token":null,"tokenPreview":null,"allowedOrigins":[],"fixedPort":null,"disconnectGraceSeconds":null,"publicBaseUrl":null}`
	if err != nil || string(raw) != want {
		t.Fatalf("public JSON changed: %s, %v", raw, err)
	}
}
