package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type worktreeDialogFixture struct {
	testSession
	prompt  *WorktreeExitPrompt
	choices []string
}

func (f *worktreeDialogFixture) PrepareWorktreeExit(context.Context) (*WorktreeExitPrompt, error) {
	return f.prompt, nil
}
func (f *worktreeDialogFixture) CompleteWorktreeExit(_ context.Context, choice string) (string, error) {
	f.choices = append(f.choices, choice)
	return "physical host result", nil
}

func TestWorktreeExitDialogDefaultsKeepCancelsAndExplicitlyRemoves(t *testing.T) {
	f := &worktreeDialogFixture{prompt: &WorktreeExitPrompt{Path: "/private/repo/.claude/worktrees/w", Branch: "worktree-w", ChangedFiles: []string{"?? proof"}, Commits: 1}}
	m := &terminalModel{state: NewState(nil, 80, 24), session: f, ctx: context.Background()}
	m.state.Closing = true
	if command := m.openWorktreeDialog(worktreePreparedMsg{prompt: f.prompt}); command != nil {
		t.Fatal("dirty worktree exited without a choice")
	}
	if m.state.WorktreeExit.selected != 0 {
		t.Fatal("keep is not default")
	}
	if command := m.handleWorktreeKey(Key{Name: "esc"}); command != nil || m.state.Closing || m.state.WorktreeExit != nil || len(f.choices) != 0 {
		t.Fatal("Escape didn't cancel exit")
	}
	m.openWorktreeDialog(worktreePreparedMsg{prompt: f.prompt})
	command := m.handleWorktreeKey(Key{Name: "enter"})
	result := command().(worktreeCompletedMsg)
	if result.err != nil || len(f.choices) != 1 || f.choices[0] != "keep" {
		t.Fatal("default keep not delegated to shared host", f.choices, result)
	}
	m.openWorktreeDialog(worktreePreparedMsg{prompt: f.prompt})
	m.handleWorktreeKey(Key{Name: "down"})
	command = m.handleWorktreeKey(Key{Name: "enter"})
	_ = command()
	if len(f.choices) != 2 || f.choices[1] != "remove" {
		t.Fatal("explicit remove missing", f.choices)
	}
}

func TestWorktreeExitDialogCleanRemovesAndSupportsOriginalTmuxChoices(t *testing.T) {
	f := &worktreeDialogFixture{prompt: &WorktreeExitPrompt{Path: "/private/w", Branch: "worktree-w"}}
	m := &terminalModel{state: NewState(nil, 80, 24), session: f, ctx: context.Background()}
	command := m.openWorktreeDialog(worktreePreparedMsg{prompt: f.prompt})
	if command == nil {
		t.Fatal("clean exit did not delegate automatic cleanup")
	}
	_ = command()
	if len(f.choices) != 1 || f.choices[0] != "remove" {
		t.Fatal(f.choices)
	}
	f.prompt.Tmux = "repo_worktree-w"
	f.prompt.ChangedFiles = []string{" M tracked"}
	if command = m.openWorktreeDialog(worktreePreparedMsg{prompt: f.prompt}); command != nil {
		t.Fatal("dirty tmux auto exited")
	}
	if got := m.state.WorktreeExit.prompt.choices(); len(got) != 3 || got[0].value != "keep-with-tmux" || got[1].value != "keep-kill-tmux" || got[2].value != "remove-with-tmux" {
		t.Fatal("original tmux choices", got)
	}
	m.handleWorktreeKey(Key{Name: "down"})
	command = m.handleWorktreeKey(Key{Name: "enter"})
	_ = command()
	if f.choices[1] != "keep-kill-tmux" {
		t.Fatal(f.choices)
	}
	// Ordered physical EOF cannot leave the terminal waiting for unavailable keys.
	_, quit := m.Update(inputEndMsg{})
	if quit == nil {
		t.Fatal("EOF trapped in dialog")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("EOF didn't release terminal")
	}
}
