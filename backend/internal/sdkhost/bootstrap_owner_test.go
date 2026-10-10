package sdkhost_test

import (
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestSDKRejectsForeignBootstrapOwnerBeforeAdmission(t *testing.T) {
	checked := false
	originalBoundary(t, nativeControls{Configure: func(options *sdkhost.Options) {
		foreign := cooperative.New()
		defer foreign.Close()
		candidate := *options
		candidate.Owner = foreign
		_, err := sdkhost.New(candidate)
		var capability *sdkhost.CapabilityError
		if !errors.As(err, &capability) || capability.Boundary != "bootstrap/controller owner mismatch" {
			t.Fatal("SDK accepted a second owner for live bootstrap state", err)
		}
		checked = true
	}})
	if !checked {
		t.Fatal("original host construction was not exercised")
	}
}
