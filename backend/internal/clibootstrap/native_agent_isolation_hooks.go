package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// Source creates an agent hook worktree on the parent session's hook boundary.
// The hook is awaited on the actual Owner, before any off-owner git work; its
// immutable returned path does not change the parent's session or cwd.
func (m *MainPrint) prepareAgentHookWorktree(ctx context.Context, scope *cooperative.Scope, agentID string) (*nativeAgentWorktree, bool, error) {
	if !scope.BelongsTo(m.Owner) || m.hook == nil || m.Controller == nil {
		return nil, false, cooperative.ErrRestricted
	}
	sid := m.Controller.SessionID()
	present, err := m.hook.HasWorktreeCreateHooks(sid)
	if err != nil || !present {
		return nil, present, err
	}
	path, err := m.hook.ExecuteWorktreeCreateHook(ctx, "agent-"+agentID[:8], sid, queryhooks.Context{Scope: scope})
	if err != nil {
		return nil, true, err
	}
	return &nativeAgentWorktree{CWD: path, Info: &worktree.AgentWorktree{Path: path, HookBased: true}}, true, nil
}
