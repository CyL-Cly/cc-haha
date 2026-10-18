package tui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Each keyed Line owns this subscription only while idle && !allIdle. It is
// separate from ClockProvider's animation and the REPL's elapsed/length refs.
type terminalTeammateSpinnerWatch struct {
	id      string
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	running sync.WaitGroup
}

type terminalTeammateSpinnerTickMsg struct {
	Now   time.Time
	watch *terminalTeammateSpinnerWatch
}

func (w *terminalTeammateSpinnerWatch) command() tea.Cmd {
	return func() tea.Msg {
		w.mu.Lock()
		if w.closed || w.ctx.Err() != nil {
			w.mu.Unlock()
			return nil
		}
		w.running.Add(1)
		w.mu.Unlock()
		defer w.running.Done()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case now := <-timer.C:
			if w.ctx.Err() != nil {
				return nil
			}
			return terminalTeammateSpinnerTickMsg{Now: now, watch: w}
		case <-w.ctx.Done():
			return nil
		}
	}
}

func (w *terminalTeammateSpinnerWatch) close() {
	w.mu.Lock()
	w.closed = true
	w.cancel()
	w.mu.Unlock()
	// Commands do no IO and never send to Tea while registered here. They
	// release before returning their message, so joining cannot await Update.
	w.running.Wait()
}

func (m *terminalModel) syncTerminalTeammateSpinnerClock() tea.Cmd {
	mounted := m.state.terminalSpinnerMounted() && m.state.Teammates != nil && m.state.Teammates.ExpandedView == "teammates"
	if m.ctx != nil && m.ctx.Err() != nil {
		mounted = false
	}
	wanted := map[string]bool{}
	order := []string{}
	if mounted {
		tasks := m.state.Teammates.Running()
		mounted = len(tasks) > 0
		allIdle := true
		for _, task := range tasks {
			allIdle = allIdle && string(task.Get("isIdle")) == "true"
		}
		if !allIdle {
			for _, task := range tasks {
				if string(task.Get("isIdle")) == "true" {
					wanted[task.String("id")] = true
					order = append(order, task.String("id"))
				}
			}
		}
	}
	for id, watch := range m.teammateSpinnerWatches {
		if !wanted[id] {
			delete(m.teammateSpinnerWatches, id)
			watch.close()
		}
	}
	if !mounted {
		m.state.clearTerminalSpinnerTree()
	}
	commands := []tea.Cmd{}
	for _, id := range order {
		if m.teammateSpinnerWatches[id] != nil {
			continue
		}
		if m.teammateSpinnerWatches == nil {
			m.teammateSpinnerWatches = map[string]*terminalTeammateSpinnerWatch{}
		}
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		life, cancel := context.WithCancel(ctx)
		watch := &terminalTeammateSpinnerWatch{id: id, ctx: life, cancel: cancel}
		m.teammateSpinnerWatches[id] = watch
		commands = append(commands, watch.command())
	}
	return tea.Batch(commands...)
}

func (m *terminalModel) terminalTeammateSpinnerTick(msg terminalTeammateSpinnerTickMsg) tea.Cmd {
	watch := msg.watch
	if watch == nil || m.teammateSpinnerWatches[watch.id] != watch || watch.ctx.Err() != nil {
		return nil
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		return nil
	}
	clock := m.state.ensureTerminalSpinnerClock()
	if r := clock.render; r != nil {
		if row := r.tree[watch.id]; row != nil && row.render != nil {
			row.rows = row.render(msg.Now)
			r.treeRows = r.composeTerminalSpinnerTree()
			r.childRevision++
		}
	}
	return watch.command()
}

func (m *terminalModel) closeTerminalTeammateSpinnerClocks() {
	for id, watch := range m.teammateSpinnerWatches {
		delete(m.teammateSpinnerWatches, id)
		watch.close()
	}
}

func (s *State) clearTerminalSpinnerTree() {
	if clock := s.spinnerClock; clock != nil && clock.render != nil {
		r := clock.render
		r.tree, r.treeRows, r.treeOrder, r.treeLeaderRows, r.treeTailRows = nil, nil, nil, nil, nil
		r.treeLeaderParts, r.treeHeaderRows = nil, nil
	}
}

func (r *terminalSpinnerRenderState) composeTerminalSpinnerTree() []string {
	rows := append([]string(nil), r.treeLeaderRows...)
	for _, id := range r.treeOrder {
		if row := r.tree[id]; row != nil {
			rows = append(rows, row.rows...)
		}
	}
	return append(rows, r.treeTailRows...)
}
