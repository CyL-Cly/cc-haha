package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// InProcessTeammateIdentity separates the outer teammate identity from the
// random runAgent sidechain ID. The original runner creates a fresh runAgent
// for each prompt while retaining the same teammate context.
type InProcessTeammateIdentity struct {
	TeamName, AgentName, AgentID string
	ParentSessionID, Color       string
	ResumableAgentID             string
	PlanModeRequired             bool
}

type InProcessTeammateContext struct {
	owner    *cooperative.Owner
	writerID string
	identity InProcessTeammateIdentity
}

func (c *Controller) InProcessWriterOnOwner(scope *cooperative.Scope, writerID string) (InProcessTeammateIdentity, error) {
	if c == nil || c.options.InProcessTeammateContext == nil || c.options.InProcessTeammateContext.writerID != writerID {
		return InProcessTeammateIdentity{}, cooperative.ErrRestricted
	}
	return c.TeammateIdentityOnOwner(scope)
}

func NewInProcessTeammateContext(scope *cooperative.Scope, owner *cooperative.Owner, writerID string, identity InProcessTeammateIdentity) (*InProcessTeammateContext, error) {
	if !scope.BelongsTo(owner) || writerID == "" || identity.TeamName == "" || identity.AgentName == "" || identity.AgentID != identity.AgentName+"@"+identity.TeamName || identity.ParentSessionID == "" || writerID == identity.AgentID {
		return nil, cooperative.ErrRestricted
	}
	return &InProcessTeammateContext{owner: owner, writerID: writerID, identity: identity}, nil
}

// TeammateIdentityOnOwner is the common readonly equivalent of the Source
// teammate helpers. It never turns an ordinary sidechain into a MAIN worker.
func (c *Controller) TeammateIdentityOnOwner(scope *cooperative.Scope) (InProcessTeammateIdentity, error) {
	if c == nil || c.options.Bootstrap == nil {
		return InProcessTeammateIdentity{}, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return InProcessTeammateIdentity{}, err
	}
	if t := c.options.InProcessTeammateContext; t != nil {
		if c.options.TeamContext != nil || t.owner != c.options.Bootstrap.Owner() || !scope.BelongsTo(t.owner) || c.options.WriterOptions.AgentID != t.writerID {
			return InProcessTeammateIdentity{}, cooperative.ErrRestricted
		}
		return t.identity, nil
	}
	team, name, id, err := c.TeamWorkerIdentity(scope)
	if t := c.options.TeamContext; t != nil && t.identity != nil {
		return *t.identity, err
	}
	return InProcessTeammateIdentity{TeamName: team, AgentName: name, AgentID: id}, err
}
