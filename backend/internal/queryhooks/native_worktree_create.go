package queryhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func (s *Service) HasWorktreeCreateHooks(sid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, e := s.snapshot()
	if e != nil {
		return false, e
	}
	var rows []json.RawMessage
	if raw := snapshot.Get("WorktreeCreate"); raw != nil {
		if e = json.Unmarshal(raw, &rows); e != nil {
			return false, e
		}
	}
	// Source tests matcher presence, even when its hooks array is empty.
	return len(rows) > 0 || len(s.nativeCommandMatchers("WorktreeCreate", sid)) > 0 || s.activeCallbacks("WorktreeCreate", sid) || len(s.sdkCallbackMatchers("WorktreeCreate")) > 0, nil
}
func (s *Service) ExecuteWorktreeCreateHook(ctx context.Context, name, sid string, call Context) (string, error) {
	call.WorktreeWrapper = true
	raw, _ := json.Marshal(name)
	results, e := s.ExecuteOutside(context.WithoutCancel(ctx), OutsideInput{Event: "WorktreeCreate", SessionID: sid, Fields: protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw}), Call: call, Timeout: 600000000000})
	if e != nil {
		return "", e
	}
	failed := []string{}
	for _, row := range results {
		var output, command string
		_ = json.Unmarshal(row.Output, &output)
		_ = json.Unmarshal(row.Command, &command)
		if row.Succeeded && strings.TrimFunc(output, javaScriptWhitespace) != "" {
			return strings.TrimFunc(output, javaScriptWhitespace), nil
		}
		if !row.Succeeded {
			output = strings.TrimFunc(output, javaScriptWhitespace)
			if output == "" {
				output = "no output"
			}
			failed = append(failed, command+": "+output)
		}
	}
	message := strings.Join(failed, "; ")
	if message == "" {
		message = "no successful output"
	}
	return "", fmt.Errorf("WorktreeCreate hook failed: %s", message)
}
