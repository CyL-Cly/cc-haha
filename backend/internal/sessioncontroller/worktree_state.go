package sessioncontroller

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SaveWorktreeStateOnOwner reuses the Controller's existing canonical writer;
// it does not borrow that writer for another SDK Host.
func (controller *Controller) SaveWorktreeStateOnOwner(scope *cooperative.Scope, session json.RawMessage) (*WriteFuture, error) {
	if controller == nil || controller.writer == nil {
		return nil, cooperative.ErrRestricted
	}
	controller.writer.mu.Lock()
	pending := controller.writer.async
	controller.writer.mu.Unlock()
	if pending == nil {
		return nil, capability("actual pending Worktree transcript writer")
	}
	return pending.BeginSaveWorktreeState(scope, session)
}

// BeginSaveWorktreeState ports saveWorktreeState, including its eager single
// metadata entry when Project.sessionFile already exists. It never materializes
// a transcript or reselects the cached physical target after a CWD transition.
func (pending *PendingWriter) BeginSaveWorktreeState(scope *cooperative.Scope, session json.RawMessage) (*WriteFuture, error) {
	if pending == nil || !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	if len(session) == 0 {
		session = json.RawMessage("null")
	}
	stripped := json.RawMessage("null")
	if string(session) != "null" {
		var object protocol.JSONObject
		if e := object.UnmarshalJSON(session); e != nil || !object.IsObject() {
			return nil, capability("actual Worktree session object")
		}
		fields := protocol.NewJSONObject()
		for _, key := range []string{"originalCwd", "worktreePath", "worktreeName", "worktreeBranch", "originalBranch", "originalHeadCommit", "sessionId", "tmuxSessionName", "hookBased"} {
			if value := object.Get(key); value != nil {
				fields.Set(key, append(json.RawMessage(nil), value...))
			}
		}
		stripped, _ = fields.MarshalJSON()
	}
	var future *WriteFuture
	err := scope.Inline(cooperative.Read, func(s *cooperative.Scope) (resultErr error) {
		if pending.closing {
			return ErrPendingClosed
		}
		writer := pending.writer
		writer.mu.Lock()
		writer.metadata.Set("worktreeSession", stripped)
		path, sid := writer.path, writer.currentSessionID()
		cache := writer.options.SettingsState.GetSessionSettingsCache()
		cleanupDisabled := false
		if cache != nil && cache.Settings != nil && cache.Settings.Get("cleanupPeriodDays") != nil {
			cleanupDisabled = string(protocol.StringifyValidatedJavaScriptJSON(cache.Settings.Get("cleanupPeriodDays"))) == "0"
		}
		files := writer.options.Files
		writer.mu.Unlock()
		future = newWriteFuture()
		if path == "" || cleanupDisabled {
			future.settle(nil)
			return nil
		}
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("worktree-state")}, protocol.JSONField{Name: "worktreeSession", Value: stripped}, protocol.JSONField{Name: "sessionId", Value: quote(sid)})
		line, e := entry.MarshalJSON()
		if e != nil {
			return e
		}
		// appendEntryToFile is synchronous in Source. Retain the exact owner
		// turn and its error propagation; generic Project enqueue/drain is a
		// distinct method and must not defer this state until the next tool.
		file, e := files.OpenFile(path, os.O_WRONLY|os.O_APPEND|noFollowFlag(), 0)
		if errors.Is(e, fs.ErrNotExist) {
			future.settle(nil)
			return nil
		}
		if e != nil {
			return e
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				resultErr = closeErr
			}
		}()
		info, e := file.Stat()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("Refusing non-regular append target: " + path)
		}
		if e = file.Chmod(0600); e != nil {
			return e
		}
		if e = writeAll(file, append(line, '\n')); e != nil {
			return e
		}
		future.settle(nil)
		return nil
	})
	return future, err
}
