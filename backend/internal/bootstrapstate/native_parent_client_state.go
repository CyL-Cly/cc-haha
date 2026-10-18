package bootstrapstate

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// NativeParentClientState reads the actual process bootstrap authority for a
// detached native child. It carries real State identities, never a replacement
// SID, callback or serialized value. Native detached admission supplies it after
// checking that child's Controller, Writer and parent constructor lineage.
type NativeParentClientState struct {
	child, parent *State
}

func BindNativeParentClientState(scope *cooperative.Scope, child, parent *State) (*NativeParentClientState, error) {
	if child == nil || parent == nil || child == parent || child.Owner() == parent.Owner() || child.SessionID() != parent.SessionID() {
		return nil, cooperative.ErrRestricted
	}
	if err := child.CheckScope(scope); err != nil {
		return nil, err
	}
	return &NativeParentClientState{child: child, parent: parent}, nil
}

func (binding *NativeParentClientState) Owns(owner *cooperative.Owner) bool {
	return binding != nil && binding.child != nil && binding.parent != nil && binding.child.Owns(owner) && binding.child != binding.parent && binding.child.Owner() != binding.parent.Owner()
}

func (binding *NativeParentClientState) SessionID(scope *cooperative.Scope) (string, error) {
	if binding == nil || !binding.Owns(binding.child.Owner()) {
		return "", cooperative.ErrRestricted
	}
	if err := binding.child.CheckScope(scope); err != nil {
		return "", err
	}
	// Snapshot takes only the parent's read lock. A child never schedules work on
	// the parent Owner, so /clear cannot create a reciprocal join dependency.
	return binding.parent.Snapshot().SessionID, nil
}
