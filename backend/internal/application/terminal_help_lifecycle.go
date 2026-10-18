package application

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type terminalHelpPanelState struct {
	args string
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error
}

func (p *terminalHelpPanelState) complete(err error) {
	if p == nil {
		return
	}
	p.once.Do(func() { p.mu.Lock(); p.err = err; p.mu.Unlock(); close(p.done) })
}

func (p *terminalHelpPanelState) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.err
	}
}

func (s *Session) closeTerminalHelpPanel(state *terminalLocalPanelState) {
	if state != nil {
		state.helpPanel.complete(cooperative.ErrClosed)
	}
}

func (s *Session) closeTerminalHelp() {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	s.closeTerminalHelpPanel(state)
}
