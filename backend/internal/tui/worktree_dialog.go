package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
)

type WorktreeExitPrompt struct {
	Path, Branch, Tmux string
	ChangedFiles       []string
	Commits            int
}
type WorktreeExitSession interface {
	PrepareWorktreeExit(context.Context) (*WorktreeExitPrompt, error)
	CompleteWorktreeExit(context.Context, string) (string, error)
}

func (m *terminalModel) completeWorktreeChoice(choice string) tea.Cmd {
	host, ok := m.session.(WorktreeExitSession)
	if !ok {
		return tea.Quit
	}
	if m.state.WorktreeExit != nil {
		m.state.WorktreeExit.pending = true
		m.state.WorktreeExit.pendingLabel = "Removing worktree…"
		if choice == "keep" || choice == "keep-with-tmux" || choice == "keep-kill-tmux" {
			m.state.WorktreeExit.pendingLabel = "Keeping worktree…"
		}
	}
	return func() tea.Msg {
		message, e := host.CompleteWorktreeExit(m.ctx, choice)
		return worktreeCompletedMsg{message, e}
	}
}
func (m *terminalModel) openWorktreeDialog(msg worktreePreparedMsg) tea.Cmd {
	if msg.err != nil {
		m.state.Status = msg.err.Error()
		m.state.Closing = false
		m.state.exitFromCommand = false
		m.allowStartingTurnsAfterCancelledExit()
		m.worktreeChecked = false
		return nil
	}
	if msg.prompt == nil {
		return m.quitWithoutWorktree()
	}
	m.state.WorktreeExit = &worktreeDialog{prompt: msg.prompt}
	if len(msg.prompt.ChangedFiles) == 0 && msg.prompt.Commits == 0 {
		return m.completeWorktreeChoice("remove")
	}
	return nil
}
func (m *terminalModel) handleWorktreeKey(key Key) tea.Cmd {
	dialog := m.state.WorktreeExit
	if dialog == nil || dialog.pending {
		return nil
	}
	choices := dialog.prompt.choices()
	switch key.Name {
	case "esc":
		m.state.WorktreeExit = nil
		m.state.Closing = false
		m.state.exitFromCommand = false
		m.allowStartingTurnsAfterCancelledExit()
		m.worktreeChecked = false
	case "up":
		dialog.selected = (dialog.selected + len(choices) - 1) % len(choices)
	case "down":
		dialog.selected = (dialog.selected + 1) % len(choices)
	case "enter":
		return m.completeWorktreeChoice(choices[dialog.selected].value)
	}
	return nil
}

type worktreeDialog struct {
	prompt       *WorktreeExitPrompt
	selected     int
	pending      bool
	pendingLabel string
}
type worktreePreparedMsg struct {
	prompt *WorktreeExitPrompt
	err    error
}
type worktreeCompletedMsg struct {
	message string
	err     error
}
type worktreeChoice struct{ value, label, description string }

func (p WorktreeExitPrompt) choices() []worktreeChoice {
	remove := "Clean up the worktree directory."
	if len(p.ChangedFiles) > 0 || p.Commits > 0 {
		remove = "All changes and commits will be lost."
	}
	if p.Tmux != "" {
		return []worktreeChoice{{"keep-with-tmux", "Keep worktree and tmux session", "Stays at " + p.Path + ". Reattach with: tmux attach -t " + p.Tmux}, {"keep-kill-tmux", "Keep worktree, kill tmux session", "Keeps worktree at " + p.Path + ", terminates tmux session."}, {"remove-with-tmux", "Remove worktree and tmux session", remove}}
	}
	return []worktreeChoice{{"keep", "Keep worktree", "Stays at " + p.Path}, {"remove", "Remove worktree", remove}}
}
func (p WorktreeExitPrompt) subtitle() string {
	files, commits := len(p.ChangedFiles), p.Commits
	fword, cword := "files", "commits"
	if files == 1 {
		fword = "file"
	}
	if commits == 1 {
		cword = "commit"
	}
	branch := p.Branch
	if branch == "" {
		branch = "undefined"
	}
	if files > 0 && commits > 0 {
		return fmt.Sprintf("You have %d uncommitted %s and %d %s on %s. All will be lost if you remove.", files, fword, commits, cword, branch)
	}
	if files > 0 {
		return fmt.Sprintf("You have %d uncommitted %s. These will be lost if you remove the worktree.", files, fword)
	}
	if commits > 0 {
		return fmt.Sprintf("You have %d %s on %s. The branch will be deleted if you remove the worktree.", commits, cword, branch)
	}
	return "You are working in a worktree. Keep it to continue working there, or remove it to clean up."
}
func (d *worktreeDialog) lines(width int) []string {
	if d.pending {
		return wrap(d.pendingLabel, width)
	}
	if d.prompt == nil {
		return nil
	}
	lines := wrap("Exiting worktree session", width)
	lines = append(lines, wrap(d.prompt.subtitle(), width)...)
	for i, c := range d.prompt.choices() {
		prefix := "  "
		if d.selected == i {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+c.label, width)...)
		lines = append(lines, wrap("  "+c.description, width)...)
	}
	return append(lines, wrap("↑/↓: select · Enter: confirm · Esc: return to session", width)...)
}
