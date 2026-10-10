package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) PrepareWorktreeExit(ctx context.Context) (*tui.WorktreeExitPrompt, error) {
	snapshot, e := s.process.PrepareWorktreeExit(ctx)
	if e != nil || snapshot == nil {
		return nil, e
	}
	s.mu.Lock()
	s.worktreeExit = snapshot
	s.mu.Unlock()
	return &tui.WorktreeExitPrompt{Path: snapshot.Session.Path, Branch: snapshot.Session.Branch, Tmux: snapshot.Session.TmuxSessionName, ChangedFiles: append([]string{}, snapshot.Status.ChangedFiles...), Commits: snapshot.Status.Commits}, nil
}
func (s *Session) CompleteWorktreeExit(ctx context.Context, choice string) (string, error) {
	s.mu.Lock()
	snapshot := s.worktreeExit
	s.worktreeExit = nil
	s.mu.Unlock()
	return s.process.CompleteWorktreeExit(ctx, snapshot, choice)
}

var _ tui.WorktreeExitSession = (*Session)(nil)
