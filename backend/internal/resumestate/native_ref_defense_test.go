package resumestate

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestNativeRefAdmissionDefenses(t *testing.T) {
	service, owner, _, refs := publicServiceReferences(t, OSFiles{})
	if _, e := service.nativeRead(nil, "never-dispatched"); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if n, _ := refs.Snapshot(); n != 0 {
		t.Fatal("nil native read admitted", n)
	}
	// Incomplete private host state is not a public supported capability. This
	// regression retains cleanup when Future creation rejects before a syscall.
	service.owned.state = &bootstrapstate.State{}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, e := service.nativeRead(scope, "never-dispatched")
		if future != nil {
			return errors.New("invalid private state created native producer")
		}
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if n, _ := refs.Snapshot(); n != 0 {
		t.Fatal("failed future creation retained physical reference", n)
	}
}
