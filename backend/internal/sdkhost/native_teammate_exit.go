package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// NativeTeammateExit belongs to the real worker Controller. It runs only after
// the ordinary yielded tool response has been recorded and projected; its
// callback requests this worker's exit without borrowing the leader's Scope.
type NativeTeammateExit struct {
	owner      *cooperative.Owner
	controller *sessioncontroller.Controller
	identity   sessioncontroller.InProcessTeammateIdentity
	writerID   string
	main       bool
	done       bool
	run        func(*cooperative.Scope) (bool, error)
}

func NewNativeTeammateExit(scope *cooperative.Scope, owner *cooperative.Owner, controller *sessioncontroller.Controller, writerID string, run func(*cooperative.Scope) (bool, error)) (*NativeTeammateExit, error) {
	if !scope.BelongsTo(owner) || controller == nil || writerID == "" || run == nil {
		return nil, cooperative.ErrRestricted
	}
	identity, err := controller.TeammateIdentityOnOwner(scope)
	if err != nil || identity.AgentID == "" {
		return nil, cooperative.ErrRestricted
	}
	_, _, mainID, err := controller.TeamWorkerIdentity(scope)
	if err != nil {
		return nil, err
	}
	main := mainID != ""
	if main {
		if mainID != writerID {
			return nil, cooperative.ErrRestricted
		}
	} else {
		if _, err = controller.InProcessWriterOnOwner(scope, writerID); err != nil {
			return nil, err
		}
	}
	return &NativeTeammateExit{owner: owner, controller: controller, identity: identity, writerID: writerID, main: main, run: run}, nil
}

func (t *NativeTeammateExit) check(scope *cooperative.Scope, owner *cooperative.Owner, controller *sessioncontroller.Controller, writerID string) (bool, error) {
	if t == nil {
		return false, nil
	}
	if !scope.BelongsTo(owner) || owner != t.owner || controller != t.controller || writerID != t.writerID {
		return false, cooperative.ErrRestricted
	}
	identity, err := controller.TeammateIdentityOnOwner(scope)
	if err != nil || identity != t.identity {
		return false, cooperative.ErrRestricted
	}
	if t.main {
		_, _, id, err := controller.TeamWorkerIdentity(scope)
		if err != nil || id != writerID {
			return false, cooperative.ErrRestricted
		}
	} else {
		if _, err = controller.InProcessWriterOnOwner(scope, writerID); err != nil {
			return false, cooperative.ErrRestricted
		}
	}
	if t.done {
		return true, nil
	}
	exit, err := t.run(scope)
	if err == nil && exit {
		t.done = true
	}
	return exit, err
}
