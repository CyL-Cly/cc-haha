package apipresentation

import (
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualNativePlainModeZeroKeepsOriginalLeafDomainRestricted(t *testing.T) {
	// The complete ordinary Source CLI consumed this exact value in five
	// parent/actual-Agent/parent HTTP controls. The old native CLI rejected it
	// before its first model request. This is an ordinary native host facet;
	// it does not expand the separately declared original bare leaf profile.
	env := originalDomainEnv()
	env["CLAUDE_CODE_SIMPLE"] = "0"
	fast := false
	domain, err := NativeDomainFromOriginalHost(requestcontext.Host{
		Profile: requestcontext.NativeDirectRelayProfile,
		Env:     env,
	}, &fast)
	if err != nil || !domain.direct || env["CLAUDE_CODE_SIMPLE"] != "0" {
		t.Fatal("actual native ordinary false mode rejected or rewrote its environment", domain, err, env)
	}
	_, err = DomainFromOriginalEnv(env)
	var capability *CapabilityError
	if !errors.As(err, &capability) || capability.Boundary != "original bare environment value" {
		t.Fatal("original finite leaf profile changed", err)
	}
}
