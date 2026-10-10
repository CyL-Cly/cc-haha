package gitbranch

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestServiceOwnerIdentityIsNilSafeAndNotLifecyclePolicy(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	service := &Service{options: Options{Owner: owner}, closed: true}
	if (*Service)(nil).OwnsOwner(owner) || service.OwnsOwner(nil) || service.OwnsOwner(foreign) || !service.OwnsOwner(owner) {
		t.Fatal("immutable owner identity is incorrect")
	}
	// Closed/cache/watch state is intentionally not sampled or used as new
	// construction policy. Existing Branch remains its owner-only lifecycle gate.
	if !service.closed {
		t.Fatal("identity check changed service")
	}
}
