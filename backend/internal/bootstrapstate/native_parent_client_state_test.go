package bootstrapstate

import (
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeParentClientStateActualPointersScopeAndCurrentSID(t *testing.T) {
	parentOwner, childOwner, foreignOwner := cooperative.New(), cooperative.New(), cooperative.New()
	defer parentOwner.Close()
	defer childOwner.Close()
	defer foreignOwner.Close()
	parent, _ := New(parentOwner, "before-clear", "/parent", func() (string, error) { return "after-clear", nil })
	child, _ := New(childOwner, "before-clear", "/child", func() (string, error) { return "child", nil })
	wrongSID, _ := New(foreignOwner, "other", "/other", func() (string, error) { return "other-next", nil })
	var binding *NativeParentClientState
	var expiredScope *cooperative.Scope
	run(t, childOwner, func(scope *cooperative.Scope) error {
		expiredScope = scope
		for _, pair := range [][2]*State{{nil, parent}, {child, nil}, {child, child}, {child, wrongSID}} {
			if _, err := BindNativeParentClientState(scope, pair[0], pair[1]); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("Unrelated/missing native state admitted", err)
			}
		}
		var err error
		binding, err = BindNativeParentClientState(scope, child, parent)
		if err != nil {
			return err
		}
		if id, e := binding.SessionID(scope); e != nil || id != "before-clear" || !binding.Owns(childOwner) || binding.Owns(parentOwner) {
			t.Error("Original child/parent authority lost", id, e)
		}
		return nil
	})
	if _, err := binding.SessionID(expiredScope); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("Expired creation scope remained active", err)
	}
	run(t, parentOwner, func(scope *cooperative.Scope) error {
		if _, err := binding.SessionID(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("Parent scope substituted for child", err)
		}
		_, err := parent.RegenerateSessionID(scope, true)
		return err
	})
	run(t, childOwner, func(scope *cooperative.Scope) error {
		id, err := binding.SessionID(scope)
		if err != nil || id != "after-clear" || child.SessionID() != "before-clear" || child.Snapshot().CWD != "/child" {
			t.Error("Snapshot mutated independent child or retained old parent SID", id, err, child.Snapshot())
		}
		if _, err := (&NativeParentClientState{}).SessionID(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("Zero value minted native authority", err)
		}
		return nil
	})
}
