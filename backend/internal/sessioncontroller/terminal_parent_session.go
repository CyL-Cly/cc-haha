package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"strings"
)

// A detached native child reads the process's actual parent bootstrap authority.
// Its own Owner, CWD overrides, permission state and history remain independent.
// There is no callback or SDK JSON field capable of providing a substitute SID.
type nativeParentSessionSnapshot struct {
	child  *bootstrapstate.State
	parent *bootstrapstate.State
	client *bootstrapstate.NativeParentClientState
}

func (c *Controller) NativeParentClientStateOnOwner(scope *cooperative.Scope) (*bootstrapstate.NativeParentClientState, error) {
	if c == nil || c.nativeParentSession == nil {
		return nil, nil
	}
	if _, err := c.nativeParentSession.client.SessionID(scope); err != nil {
		return nil, err
	}
	return c.nativeParentSession.client, nil
}

func (c *Controller) RefreshNativeParentRequestOnOwner(scope *cooperative.Scope, request *requestcontext.Context) error {
	b := c.nativeParentSession
	if b == nil {
		return nil
	}
	if request == nil || !scope.BelongsTo(b.child.Owner()) || c.options.Bootstrap != b.child {
		return cooperative.ErrRestricted
	}
	request.Host.SessionID = b.parent.SessionID()
	return nil
}

func (c *Controller) BindNativeParentSessionOnOwner(scope *cooperative.Scope, parent *bootstrapstate.State) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || parent == nil || parent == c.options.Bootstrap || parent.Owner() == c.options.Bootstrap.Owner() || !c.options.NativeTools || !c.options.PublicPrint || !c.options.AsyncPersistence || c.options.WriterOptions.AgentID == "" || c.writer.async == nil || c.writer.async.owner != c.options.Bootstrap.Owner() || c.nativeParentSession != nil {
		return cooperative.ErrRestricted
	}
	if c.options.QuerySource != "agent:custom" && !strings.HasPrefix(c.options.QuerySource, "agent:builtin:") && c.options.QuerySource != "workflow_agent" {
		return cooperative.ErrRestricted
	}
	if c.options.Bootstrap.SessionID() != parent.SessionID() {
		return cooperative.ErrRestricted
	}
	client, err := bootstrapstate.BindNativeParentClientState(scope, c.options.Bootstrap, parent)
	if err != nil {
		return err
	}
	binding := &nativeParentSessionSnapshot{child: c.options.Bootstrap, parent: parent, client: client}
	c.nativeParentSession = binding
	c.writer.nativeParentSession = binding
	return nil
}

// Called on the existing child Owner before a new Record. Source /clear resets
// the process Project pointer; an already-admitted Append still owns its old
// immutable path and transport. Neither child nor parent waits for the other.
func (w *Writer) refreshNativeParentSessionOnOwner(scope *cooperative.Scope) error {
	b := w.nativeParentSession
	if b == nil {
		return nil
	}
	if !scope.BelongsTo(b.child.Owner()) || w.options.Bootstrap != b.child {
		return cooperative.ErrRestricted
	}
	snapshot := b.parent.Snapshot()
	w.mu.Lock()
	defer w.mu.Unlock()
	if snapshot.SessionID != w.options.SessionID {
		w.reset()
		w.options.SessionID = snapshot.SessionID
		w.options.ProjectDir = ""
		w.excluded = w.exclusions(snapshot.SessionID)
	}
	return nil
}
