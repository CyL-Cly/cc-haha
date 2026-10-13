package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func (m *MainPrint) nativeRestoreWorktreeForResume(scope *cooperative.Scope, raw json.RawMessage, fork bool) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if !m.options.Native.NativeProcess {
		return nil
	}
	// The public print loadInitialMessages branch restores metadata but does
	// not call the interactive processResumedConversation worktree/adoption
	// consumer. Its next user turn uses the normal materialization path.
	if m.options.PublicPrintLoadErrors {
		return nil
	}
	if fork {
		return nil
	}
	if m.worktreeState != nil && m.worktreeState.current != nil {
		// A freshly requested --worktree takes precedence over historical state.
		if e := m.saveTranscriptWorktree(scope, m.worktreeState.current); e != nil {
			return e
		}
	} else if raw != nil && string(raw) != "null" && string(raw) != "false" && string(raw) != "0" && string(raw) != `""` {
		var object protocol.JSONObject
		var session worktree.Session
		if object.UnmarshalJSON(raw) != nil || !object.IsObject() || json.Unmarshal(raw, &session) != nil || session.Path == "" {
			if e := m.saveTranscriptWorktree(scope, nil); e != nil {
				return e
			}
		} else {
			if e := m.setWorkingDirectory(scope, session.Path); e != nil {
				if e = m.saveTranscriptWorktree(scope, nil); e != nil {
					return e
				}
			} else {
				if e = m.state.SetOriginalCWD(scope, m.workingDirectory()); e != nil {
					return e
				}
				if m.worktreeState == nil {
					m.worktreeState = &nativeWorktreeState{}
				}
				m.worktreeState.current = &session
				if e = m.clearWorktreeCaches(scope); e != nil {
					return e
				}
			}
		}
	}
	return m.Controller.AdoptNativeResumedTranscript(scope)
}
