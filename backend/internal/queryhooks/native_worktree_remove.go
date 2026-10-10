package queryhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) ExecuteWorktreeRemoveHook(ctx context.Context, path, sid string, call Context) (bool, error) {
	s.mu.Lock()
	snapshot, err := s.snapshot()
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	var rows []json.RawMessage
	if raw := snapshot.Get("WorktreeRemove"); raw != nil {
		err = json.Unmarshal(raw, &rows)
	}
	has := len(rows) > 0 || len(s.nativeCommandMatchers("WorktreeRemove", sid)) > 0 || s.activeCallbacks("WorktreeRemove", sid) || len(s.sdkCallbackMatchers("WorktreeRemove")) > 0
	s.mu.Unlock()
	if err != nil || !has {
		return false, err
	}
	call.WorktreeWrapper = true
	results, err := s.ExecuteOutside(context.WithoutCancel(ctx), OutsideInput{Event: "WorktreeRemove", SessionID: sid, Fields: protocol.NewJSONObject(protocol.JSONField{Name: "worktree_path", Value: commandJSON(path)}), Call: call, Timeout: 600000 * time.Millisecond})
	return len(results) > 0, err
}
