package tui

import (
	"context"
	"math"
	"math/rand"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ExitReasonSession delegates to the same session's cancel/drain/cleanup path.
// The terminal must not kill the process or construct another Agent to exit.
type ExitReasonSession interface {
	CloseWithReason(context.Context, string) error
}

func (s *State) exitInput(input Input) ([]action, bool) {
	if input.Mode != "prompt" {
		return nil, false
	}
	parsed := ParseSlashCommand(input.Text)
	if parsed == nil || parsed.CommandName != "exit" && parsed.CommandName != "quit" {
		return nil, false
	}
	s.exitFromCommand = true
	s.Editor.Reset()
	s.PastedContents = nil
	s.pendingImageSpace = false
	s.Completions = nil
	s.ArgumentHint = ""
	return s.EOF(), true
}

func exitGoodbye(random float64) string {
	messages := []string{"Goodbye!", "See ya!", "Bye!", "Catch you later!"}
	index := int(math.Floor(random * float64(len(messages))))
	if index < 0 || index >= len(messages) {
		return "Goodbye!"
	}
	return messages[index]
}

func (m *terminalModel) quitWithoutWorktree() tea.Cmd {
	if m.state.exitFromCommand {
		m.state.Entries = append(m.state.Entries, Entry{Kind: "notice", Text: exitGoodbye(rand.Float64())})
	}
	return tea.Quit
}

// Exit closes terminal admission before waiting, so a delayed submit Cmd cannot
// start a new ASK while an earlier ASK's cancellation/finally is draining.
func (m *terminalModel) prepareExitAfterTurns(host WorktreeExitSession) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		done := m.currentTurnsDrained()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return worktreePreparedMsg{err: ctx.Err()}
			}
		}
		prompt, err := host.PrepareWorktreeExit(ctx)
		return worktreePreparedMsg{prompt, err}
	}
}

func (m *terminalModel) allowStartingTurnsAfterCancelledExit() {
	m.lifetime.Lock()
	defer m.lifetime.Unlock()
	if m.ctx.Err() == nil {
		m.closed = false
	}
}

func (m *terminalModel) endTurn() {
	m.lifetime.Lock()
	defer m.lifetime.Unlock()
	m.turns.Done()
	m.activeTurns--
	if m.activeTurns == 0 {
		close(m.turnsDrained)
	}
}
func (m *terminalModel) currentTurnsDrained() <-chan struct{} {
	m.lifetime.Lock()
	defer m.lifetime.Unlock()
	if m.activeTurns == 0 {
		return nil
	}
	return m.turnsDrained
}
