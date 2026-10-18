package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func (m *MainPrint) clearTerminalResumeWorktreeCaches(scope *cooperative.Scope) error {
	if m.userContext != nil {
		m.userContext.ClearWorktreeMemoryFiles()
	}
	if m.memoryPaths != nil {
		m.memoryPaths.Clear()
	}
	return m.clearWorktreeCaches(scope)
}

// Mid-session resume first exits the previous restored worktree. Startup's
// freshly requested --worktree preference is a different consumer.
func (m *MainPrint) restoreTerminalResumeWorktree(scope *cooperative.Scope, metadata json.RawMessage) error {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.worktreeState != nil && m.worktreeState.current != nil {
		previous := m.worktreeState.current
		m.worktreeState.current = nil
		if err := m.clearTerminalResumeWorktreeCaches(scope); err != nil {
			return err
		}
		// Source catches a failed chdir when the original directory vanished.
		// The target restore below is still attempted from the surviving cwd.
		if err := m.setWorkingDirectory(scope, previous.OriginalCWD); err == nil {
			if err = m.state.SetOriginalCWD(scope, m.workingDirectory()); err != nil {
				return err
			}
			if err = m.clearWorktreeCaches(scope); err != nil {
				return err
			}
		}
	}
	if len(metadata) == 0 || string(metadata) == "null" || string(metadata) == "false" || string(metadata) == "0" || string(metadata) == `""` {
		return m.Controller.AdoptNativeResumedTranscript(scope)
	}
	var fields protocol.JSONObject
	var restored worktree.Session
	if err := json.Unmarshal(metadata, &fields); err != nil || !fields.IsObject() || json.Unmarshal(metadata, &restored) != nil || restored.Path == "" {
		if err := m.saveTranscriptWorktree(scope, nil); err != nil {
			return err
		}
		return m.Controller.AdoptNativeResumedTranscript(scope)
	}
	if err := m.setWorkingDirectory(scope, restored.Path); err != nil {
		if err = m.saveTranscriptWorktree(scope, nil); err != nil {
			return err
		}
		return m.Controller.AdoptNativeResumedTranscript(scope)
	}
	if err := m.state.SetOriginalCWD(scope, m.workingDirectory()); err != nil {
		return err
	}
	if m.worktreeState == nil {
		m.worktreeState = &nativeWorktreeState{}
	}
	m.worktreeState.current = &restored
	if err := m.clearTerminalResumeWorktreeCaches(scope); err != nil {
		return err
	}
	return m.Controller.AdoptNativeResumedTranscript(scope)
}
