package requestcontext

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeParentMetadataRejectsFiniteOrForeignAuthority(t *testing.T) {
	parentOwner, childOwner := cooperative.New(), cooperative.New()
	defer parentOwner.Close()
	defer childOwner.Close()
	parent, _ := bootstrapstate.New(parentOwner, "original", "/parent", func() (string, error) { return "cleared", nil })
	child, _ := bootstrapstate.New(childOwner, "original", "/child", func() (string, error) { return "child", nil })
	var binding *bootstrapstate.NativeParentClientState
	if err := childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		binding, e = bootstrapstate.BindNativeParentClientState(scope, child, parent)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	finite, err := Prepare(basicContext(t), dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if err = childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := finite.RefreshNativeParentMetadataOnOwner(scope, binding); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("Finite request accepted native parent", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := basicContext(t)
	input.Host.Profile = NativeDirectRelayProfile
	prepared, err := Prepare(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	if err = parentOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := prepared.RefreshNativeParentMetadataOnOwner(scope, binding); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("Foreign Owner overwrote metadata", e)
		}
		_, e := parent.RegenerateSessionID(scope, true)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = childOwner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := prepared.RefreshNativeParentMetadataOnOwner(scope, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("Missing native authority admitted", e)
		}
		if e := prepared.RefreshNativeParentMetadataOnOwner(scope, binding); e != nil {
			return e
		}
		if prepared.input.Host.SessionID != "cleared" || input.Host.SessionID == "cleared" {
			t.Error("Metadata did not sample actual parent or changed input", prepared.input.Host.SessionID, input.Host.SessionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
