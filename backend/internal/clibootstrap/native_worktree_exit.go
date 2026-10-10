package clibootstrap

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type WorktreeExitSnapshot struct {
	Session worktree.Session
	Status  worktree.ExitStatus
}

func (m *MainPrint) PrepareWorktreeExit(ctx context.Context) (*WorktreeExitSnapshot, error) {
	var snapshot *WorktreeExitSnapshot
	var cwd string
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.worktreeState == nil || m.worktreeState.current == nil {
			return nil
		}
		snapshot = &WorktreeExitSnapshot{Session: *m.worktreeState.current}
		cwd = m.workingDirectory()
		return nil
	})
	if e != nil || snapshot == nil {
		return snapshot, e
	}
	service, e := m.nativeWorktreeService()
	if e != nil {
		return nil, e
	}
	snapshot.Status, e = service.InspectExitDialog(ctx, cwd, snapshot.Session)
	return snapshot, e
}

func (m *MainPrint) CompleteWorktreeExit(ctx context.Context, snapshot *WorktreeExitSnapshot, choice string) (string, error) {
	if snapshot == nil {
		return "No active worktree session found", nil
	}
	service, e := m.nativeWorktreeService()
	if e != nil {
		return "", e
	}
	keep := choice == "keep" || choice == "keep-with-tmux" || choice == "keep-kill-tmux"
	remove := choice == "remove" || choice == "remove-with-tmux"
	if !keep && !remove {
		return "", fmt.Errorf("Invalid worktree exit choice: %s", choice)
	}
	if snapshot.Session.TmuxSessionName != "" && (remove || choice == "keep-kill-tmux") {
		if _, e = service.KillTmux(ctx, snapshot.Session.TmuxSessionName); e != nil {
			return "", e
		}
	}
	clear := true
	if remove {
		clear, e = service.Remove(ctx, snapshot.Session)
		if e != nil {
			return "Worktree cleanup failed, exiting anyway", nil
		}
	}
	e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if clear {
			m.worktreeState.current = nil
			if e := m.saveActiveWorktree(nil, snapshot.Session.OriginalCWD); e != nil {
				return e
			}
		}
		if e := m.setWorkingDirectory(scope, snapshot.Session.OriginalCWD); e != nil {
			return e
		}
		if e := m.saveTranscriptWorktree(scope, nil); e != nil {
			return e
		}
		return m.plans.SetWorkingDirectory(scope, m.workingDirectory())
	})
	if e != nil {
		if remove {
			return "Worktree cleanup failed, exiting anyway", nil
		}
		return "", e
	}
	s := snapshot.Session
	branch := s.Branch
	if branch == "" {
		branch = "undefined"
	}
	if keep {
		if choice == "keep-kill-tmux" {
			return fmt.Sprintf("Worktree kept at %s on branch %s. Tmux session terminated.", s.Path, branch), nil
		}
		message := fmt.Sprintf("Worktree kept. Your work is saved at %s on branch %s", s.Path, branch)
		if s.TmuxSessionName != "" {
			message += ". Reattach to tmux session with: tmux attach -t " + s.TmuxSessionName
		}
		return message, nil
	}
	status := snapshot.Status
	if status.Commits == 0 && len(status.ChangedFiles) == 0 {
		return "Worktree removed (no changes)", nil
	}
	tmux := ""
	if s.TmuxSessionName != "" {
		tmux = " Tmux session terminated."
	}
	word := "commits"
	verb := "were"
	if status.Commits == 1 {
		word = "commit"
		verb = "was"
	}
	if status.Commits > 0 && len(status.ChangedFiles) > 0 {
		return fmt.Sprintf("Worktree removed. %d %s and uncommitted changes were discarded.%s", status.Commits, word, tmux), nil
	}
	if status.Commits > 0 {
		return fmt.Sprintf("Worktree removed. %d %s on %s %s discarded.%s", status.Commits, word, branch, verb, tmux), nil
	}
	if len(status.ChangedFiles) > 0 {
		return "Worktree removed. Uncommitted changes were discarded." + tmux, nil
	}
	return "Worktree removed." + tmux, nil
}
