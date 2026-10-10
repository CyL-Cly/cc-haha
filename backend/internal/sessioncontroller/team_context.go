package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

// TeamContext belongs to a standalone worker's main Controller. Its identity
// is independent of WriterOptions.AgentID, which selects a sidechain file.
type TeamContext struct {
	owner               *cooperative.Owner
	team, name, agentID string
	identity            *InProcessTeammateIdentity
}

func NewTeamContext(owner *cooperative.Owner, team, name, agentID string) (*TeamContext, error) {
	if owner == nil || team == "" || name == "" || agentID != name+"@"+team {
		return nil, cooperative.ErrRestricted
	}
	return &TeamContext{owner: owner, team: team, name: name, agentID: agentID}, nil
}
func (c *Controller) TeamWorkerIdentity(scope *cooperative.Scope) (team, name, agentID string, err error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return "", "", "", cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return "", "", "", err
	}
	if t := c.options.TeamContext; t != nil {
		if !scope.BelongsTo(t.owner) || t.owner != c.options.Bootstrap.Owner() || c.options.WriterOptions.AgentID != "" {
			return "", "", "", cooperative.ErrRestricted
		}
		return t.team, t.name, t.agentID, nil
	}
	return "", "", "", nil
}
func (c *Controller) SetLeaderTeamOnOwner(scope *cooperative.Scope, name string) error {
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	if c.options.TeamContext != nil || c.options.InProcessTeammateContext != nil {
		return cooperative.ErrRestricted
	}
	c.leaderTeamName = name
	if name == "" {
		c.appState.Inbox = nil
	}
	return nil
}
func (c *Controller) teamTaskList(scope *cooperative.Scope) (string, string, error) {
	identity, err := c.TeammateIdentityOnOwner(scope)
	team, agent := identity.TeamName, identity.AgentID
	if err != nil {
		return "", "", err
	}
	if team == "" {
		team = c.leaderTeamName
	}
	if c.options.InProcessTeammateContext != nil {
		agent = c.options.WriterOptions.AgentID
	}
	if team != "" {
		return teamstore.SanitizeName(team), agent, nil
	}
	return "", agent, nil
}

func NewPaneTeamContext(owner *cooperative.Owner, identity InProcessTeammateIdentity) (*TeamContext, error) {
	t, err := NewTeamContext(owner, identity.TeamName, identity.AgentName, identity.AgentID)
	if err != nil {
		return nil, err
	}
	t.identity = &identity
	return t, nil
}
