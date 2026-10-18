package requestcontext

import (
	"bytes"
	"testing"
)

func TestNativeExternalUserUsesOrdinaryRequestWhileInternalAndFiniteRemainGuarded(t *testing.T) {
	base := basicContext(t)
	base.Host.Profile = NativeDirectRelayProfile
	want, err := Build(base, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"external", "", "EXTERNAL", "Ant"} {
		input := basicContext(t)
		input.Host.Profile = NativeDirectRelayProfile
		input.Host.Env["USER_TYPE"] = value
		actual, err := Build(input, dependencies())
		if err != nil {
			t.Fatal(value, err)
		}
		if !bytes.Equal(actual.PreparedRequest.Body, want.PreparedRequest.Body) {
			t.Fatal("ordinary identity changed request", value)
		}
	}
	for _, profile := range []string{DirectRelayProfile, NativeDirectRelayProfile} {
		input := basicContext(t)
		input.Host.Profile = profile
		input.Host.Env["USER_TYPE"] = "ant"
		if _, err := Build(input, dependencies()); err == nil {
			t.Fatal("unresolved internal producer admitted", profile)
		}
	}
	finite := basicContext(t)
	finite.Host.Env["USER_TYPE"] = "external"
	if _, err := Build(finite, dependencies()); err == nil {
		t.Fatal("finite profile broadened")
	}
}
