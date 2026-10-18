package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestRemoteTitleAuthorityRequiresCanonicalOwnerAndPreservesReadFrameBoundaries(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var authority *NativeRemoteTitleModels
	var stale *Scope
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var err error
		authority, err = NewNativeRemoteTitleModels(scope, owner)
		if err != nil {
			return err
		}
		if _, err := NewNativeRemoteTitleModels(scope, foreign); !errors.Is(err, ErrRestricted) {
			t.Error("foreign constructor admitted", err)
		}
		return scope.Inline(Read, func(read *Scope) error {
			if _, err := NewNativeRemoteTitleModels(read, owner); !errors.Is(err, ErrRestricted) {
				t.Error("read callback minted transport", err)
			}
			if err := read.Frame(FrameFeed, func() error { return nil }); !errors.Is(err, ErrRestricted) {
				t.Error("generic frame guard changed", err)
			}
			return authority.Run(read, context.Background(), func(producer *Scope) error {
				return producer.Frame(FrameFeed, func() error { return nil })
			})
		})
	}); err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []string{"teleport_generate_title", "rename_generate_name", "generate_session_title"} {
		if !authority.AcceptsPurpose(purpose) {
			t.Fatal("canonical queryHaiku purpose rejected", purpose)
		}
	}
	for _, purpose := range []string{"", "ordinary_user", "teleport_generate_title_extra", "rename_generate_name_extra", "generate_session_title_extra"} {
		if authority.AcceptsPurpose(purpose) {
			t.Fatal("arbitrary querySource admitted", purpose)
		}
	}
	if err := authority.Run(stale, context.Background(), func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal("stale callback admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		if err := authority.Run(scope, context.Background(), func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
			t.Error("foreign execution admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var nilAuthority *NativeRemoteTitleModels
	if nilAuthority.Owns(owner) || authority.Owns(foreign) || !errors.Is(nilAuthority.Run(stale, context.Background(), nil), ErrRestricted) {
		t.Fatal("nil/foreign authority accepted")
	}
}
