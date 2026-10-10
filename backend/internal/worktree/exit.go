package worktree

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Changes struct{ Files, Commits int }

func (s *Service) CountChanges(ctx context.Context, session Session) (*Changes, error) {
	result, e := s.run(ctx, session.Path, []string{"status", "--porcelain"}, false)
	if e != nil {
		return nil, e
	}
	if result.Code != 0 || session.OriginalHeadCommit == "" {
		return nil, nil
	}
	files := 0
	for _, line := range strings.Split(result.Stdout, "\n") {
		if trim(line) != "" {
			files++
		}
	}
	result, e = s.run(ctx, session.Path, []string{"rev-list", "--count", session.OriginalHeadCommit + "..HEAD"}, false)
	if e != nil {
		return nil, e
	}
	if result.Code != 0 {
		return nil, nil
	}
	commits := 0
	_, _ = fmt.Sscan(trim(result.Stdout), &commits)
	return &Changes{files, commits}, nil
}
func (s *Service) ValidateRemove(ctx context.Context, session *Session, discard bool) (int, error) {
	if session == nil {
		return 1, errors.New("No-op: there is no active EnterWorktree session to exit. This tool only operates on worktrees created by EnterWorktree in the current session — it will not touch worktrees created manually or in a previous session. No filesystem changes were made.")
	}
	if discard {
		return 0, nil
	}
	summary, e := s.CountChanges(ctx, *session)
	if e != nil {
		return 0, e
	}
	if summary == nil {
		return 3, fmt.Errorf("Could not verify worktree state at %s. Refusing to remove without explicit confirmation. Re-invoke with discard_changes: true to proceed — or use action: \"keep\" to preserve the worktree.", session.Path)
	}
	if summary.Files > 0 || summary.Commits > 0 {
		parts := []string{}
		if summary.Files > 0 {
			word := "files"
			if summary.Files == 1 {
				word = "file"
			}
			parts = append(parts, fmt.Sprintf("%d uncommitted %s", summary.Files, word))
		}
		if summary.Commits > 0 {
			word := "commits"
			if summary.Commits == 1 {
				word = "commit"
			}
			branch := session.Branch
			if branch == "" {
				branch = "the worktree branch"
			}
			parts = append(parts, fmt.Sprintf("%d %s on %s", summary.Commits, word, branch))
		}
		return 2, fmt.Errorf("Worktree has %s. Removing will discard this work permanently. Confirm with the user, then re-invoke with discard_changes: true — or use action: \"keep\" to preserve the worktree.", strings.Join(parts, " and "))
	}
	return 0, nil
}

// Remove ports cleanupWorktree's actual command/log behavior. A false return
// means its outer catch retained the active worktree; callers must preserve it.
func (s *Service) Remove(ctx context.Context, session Session) (bool, error) {
	if session.HookBased {
		if s.options.RemoveHook == nil {
			return false, &CapabilityError{"actual configured WorktreeRemove hooks"}
		}
		ran, e := s.options.RemoveHook(ctx, session.Path)
		if e != nil {
			s.options.Log("error", "Error cleaning up worktree: "+e.Error())
			return false, nil
		}
		if !ran {
			s.options.Log("warn", "No WorktreeRemove hook configured, hook-based worktree left at: "+session.Path)
		}
		return true, nil
	}
	root := s.GitRoot(session.OriginalCWD)
	if root == "" {
		s.options.Log("error", fmt.Sprintf("Error cleaning up worktree: Refusing to remove worktree %q: the original repository cannot be resolved", session.Path))
		return false, nil
	}
	if e := s.SafeLocation(root, session.Path); e != nil {
		s.options.Log("error", "Error cleaning up worktree: "+e.Error())
		return false, nil
	}
	result, e := s.run(ctx, session.OriginalCWD, []string{"worktree", "remove", "--force", session.Path}, false)
	if e != nil {
		s.options.Log("error", "Error cleaning up worktree: "+e.Error())
		return false, nil
	}
	if result.Code != 0 {
		s.options.Log("error", "Failed to remove linked worktree: "+result.Stderr)
	}
	if session.Branch != "" {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return false, context.Cause(ctx)
		}
		result, e = s.run(ctx, session.OriginalCWD, []string{"branch", "-D", session.Branch}, false)
		if e != nil {
			s.options.Log("error", "Error cleaning up worktree: "+e.Error())
			return true, nil
		}
		if result.Code != 0 {
			s.options.Log("error", "Could not delete worktree branch: "+result.Stderr)
		}
	}
	return true, nil
}
func (s *Service) KillTmux(ctx context.Context, name string) (bool, error) {
	result, e := s.options.Run(ctx, ProcessRequest{Command: "tmux", Arguments: []string{"kill-session", "-t", name}, Environment: s.options.Environment})
	return result.Code == 0, e
}
