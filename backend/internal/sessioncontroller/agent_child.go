package sessioncontroller

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// NewAgentChild shares process services and accounting while giving the
// sidechain its own original message store, read cache and physical Writer.
func (c *Controller) NewAgentChild(scope *cooperative.Scope, state *bootstrapstate.State, branch *OwnedRecordBranch, agentID string, prompt func(context.Context, []protocol.JSONObject) ([]json.RawMessage, error)) (*Controller, error) {
	return c.NewConfiguredAgentChild(scope, state, branch, agentID, prompt, AgentChildOptions{QuerySource: "agent:builtin:general-purpose"})
}

type AgentChildOptions struct {
	QuerySource, PermissionMode string
	OmitClaudeMd                bool
}

func (c *Controller) NewConfiguredAgentChild(scope *cooperative.Scope, state *bootstrapstate.State, branch *OwnedRecordBranch, agentID string, prompt func(context.Context, []protocol.JSONObject) ([]json.RawMessage, error), child AgentChildOptions) (*Controller, error) {
	if c == nil || state == nil || !c.BootstrapOwns(state.Owner()) || !scope.BelongsTo(state.Owner()) || !c.active || !c.options.NativeTools || agentID == "" || strings.ContainsAny(agentID, "/\\\x00") || prompt == nil {
		return nil, cooperative.ErrRestricted
	}
	o := c.options
	o.Bootstrap = state
	o.BeginOwnedRecordBranch = branch
	o.ResumeOptions.CurrentSessionID = state.SessionID()
	o.ResumeOptions.OnSnapshotRecord = nil
	o.WriterOptions.Bootstrap = state
	o.WriterOptions.SessionID = state.SessionID()
	o.WriterOptions.AgentID = agentID
	o.WriterOptions.Branch = nil
	o.ResolveSystemPrompt = prompt
	o.ResolveSystemPromptForModel = nil
	o.CustomSystemPrompt = nil
	o.ReplayUserMessages = false
	if child.QuerySource != "agent:custom" && !strings.HasPrefix(child.QuerySource, "agent:builtin:") {
		return nil, cooperative.ErrRestricted
	}
	o.QuerySource = child.QuerySource
	o.AgentPermissionMode = child.PermissionMode
	o.AgentOmitClaudeMd = child.OmitClaudeMd
	o.RestoreCosts = nil
	o.SharedAccounting = c.modelAccounting
	o.InheritedPermissions = c
	return New(o)
}

// Source runAgent creates an empty non-fork sidechain, independent of the
// main CLI's loadInitialMessages/SessionStart startup branch.
func (c *Controller) ActivateAgentOwned(scope *cooperative.Scope) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || (c.options.QuerySource != "agent:custom" && !strings.HasPrefix(c.options.QuerySource, "agent:builtin:") && !(c.options.NativeTools && c.options.QuerySource == "workflow_agent")) || c.options.WriterOptions.AgentID == "" || c.active {
		return cooperative.ErrRestricted
	}
	c.messages = nil
	c.readCache = newReadCache(headlessReadCacheEntries)
	c.active = true
	c.freshOwned = true
	c.options.OnPhase("agent:initial-messages")
	return nil
}
