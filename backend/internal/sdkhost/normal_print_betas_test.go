package sdkhost

import (
	"bytes"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// This projection check complements the real caller regression. An undefined
// own slot must survive internally while only its bytes disappear at encoding.
// Legacy explicit empty arrays are a separate frozen SDK profile.
func TestNormalPrintInitUndefinedSlotPreservesLegacySDKBetaBytes(t *testing.T) {
	options := Options{Controller: &sessioncontroller.Controller{}, Model: "fixture-model", Version: "fixture-version", CWD: "fixture-cwd", UUID: func() string { return "fixture-uuid" }}
	legacy := (&Host{options: options}).initMessage()
	if string(legacy.Get("betas")) != "[]" {
		t.Fatal("legacy explicit empty SDK array changed", string(legacy.Get("betas")))
	}
	legacyBytes, err := legacy.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	options.Print = &PrintOptions{Registry: &PrintRegistry{}}
	normal := (&Host{options: options}).initMessage()
	ownUndefined := false
	for _, property := range normal.OwnProperties() {
		if string(property.NameJSON) == `"betas"` {
			ownUndefined = property.Value == nil
		}
	}
	if !ownUndefined {
		t.Fatal("normal init must own betas:undefined rather than explicit [] or deleted property")
	}
	normalBytes, err := normal.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(legacyBytes, []byte(`,"betas":[]`), nil, 1)
	if !bytes.Equal(normalBytes, want) {
		t.Fatalf("only undefined betas encoding may differ; want=%s actual=%s", want, normalBytes)
	}
	if bytes.Contains(normalBytes, []byte(`"betas"`)) || !bytes.Contains(legacyBytes, []byte(`"betas":[]`)) {
		t.Fatal("undefined and explicit empty array became indistinguishable", string(normalBytes), string(legacyBytes))
	}
}
