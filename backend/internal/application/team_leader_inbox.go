package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"time"
)

type sessionLeaderInbox struct {
	cancel context.CancelFunc
	done   chan struct{}
	emit   tui.Emit
	wake   chan struct{}
}

func (s *Session) interactiveTeamTurn(started bool) error {
	if err := s.emitEvent(tui.Event{Kind: tui.TeammateInboxChanged, Result: raw(s.process.Controller.AppState().Inbox)}); err != nil {
		return err
	}
	kind := tui.TeammateTurnFinished
	if started {
		kind = tui.TeammateTurnStarted
	}
	return s.emitEvent(tui.Event{Kind: kind})
}

// startLeaderInbox is called from the actual leader's post-turn owner stack.
// The idle job never holds turnMu while waiting for human input and uses the
// existing native Host for mailbox follow-up prompts, permissions and records.
func (s *Session) startLeaderInbox() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	if current := s.leaderInbox; current != nil {
		select {
		case <-current.done:
		default:
			if current.wake != nil {
				select {
				case current.wake <- struct{}{}:
				default:
				}
			}
			s.mu.Unlock()
			return nil
		}
	}
	life, cancel := context.WithCancel(context.Background())
	state := &sessionLeaderInbox{cancel: cancel, done: make(chan struct{}), emit: s.emit}
	s.leaderInbox = state
	s.mu.Unlock()
	go s.runLeaderInbox(life, state)
	return nil
}

func (s *Session) startInteractiveLeaderInbox() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	life, cancel := context.WithCancel(context.Background())
	state := &sessionLeaderInbox{cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	s.leaderInbox = state
	s.mu.Unlock()
	go s.runInteractiveLeaderInbox(life, state)
	return nil
}

func (s *Session) runInteractiveLeaderInbox(life context.Context, state *sessionLeaderInbox) {
	defer close(state.done)
	defer state.cancel()
	ticker := time.NewTicker(time.Duration(teamspawn.InteractiveInboxPollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for life.Err() == nil {
		inbox, err := s.process.PollInteractiveLeaderInbox(life)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, cooperative.ErrClosed) {
				return
			}
			if s.leaderInboxError != nil {
				_, _ = fmt.Fprintln(s.leaderInboxError, err)
			}
			select {
			case <-life.Done():
				return
			case <-ticker.C:
			case <-state.wake:
			}
			continue
		}
		_ = s.emitEvent(tui.Event{Kind: tui.TeammateInboxChanged, Result: raw(inbox)})
		s.mu.Lock()
		canStart := s.canStartTerminalTeamInboxLocked(inbox)
		s.mu.Unlock()
		if canStart && s.turnMu.TryLock() {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				s.turnMu.Unlock()
				return
			}
			if !s.canStartTerminalTeamInboxLocked(inbox) {
				s.mu.Unlock()
				s.turnMu.Unlock()
				select {
				case <-life.Done():
					return
				case <-ticker.C:
				case <-state.wake:
				}
				continue
			}
			work, cancel := context.WithCancel(life)
			id, idErr := nativeDesktopTeamUUID()
			if idErr != nil {
				cancel()
				s.mu.Unlock()
				s.turnMu.Unlock()
				return
			}
			s.cancel, s.turnID, s.emit = cancel, "teammate-inbox-"+id, nil
			s.mu.Unlock()
			_, err = s.process.RunLeaderInboxCycle(work)
			cancel()
			s.mu.Lock()
			s.cancel, s.turnID, s.emit = nil, "", nil
			s.mu.Unlock()
			s.turnMu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, cooperative.ErrClosed) && !errors.Is(err, cooperative.ErrRestricted) {
				if s.leaderInboxError != nil {
					_, _ = fmt.Fprintln(s.leaderInboxError, err)
				}
				return
			}
		}
		select {
		case <-life.Done():
			return
		case <-ticker.C:
		case <-state.wake:
		}
	}
}

func (s *Session) runLeaderInbox(life context.Context, state *sessionLeaderInbox) {
	defer close(state.done)
	defer state.cancel()
	for life.Err() == nil {
		s.turnMu.Lock()
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			s.turnMu.Unlock()
			return
		}
		work, cancel := context.WithCancel(life)
		s.cancel = cancel
		s.turnID = ""
		s.emit = state.emit
		s.mu.Unlock()
		active, err := s.process.RunLeaderInboxCycle(work)
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.emit = nil
		s.mu.Unlock()
		s.turnMu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, cooperative.ErrClosed) {
			if s.leaderInboxError != nil {
				_, _ = fmt.Fprintln(s.leaderInboxError, err)
			}
			return
		}
		if !active {
			return
		}
	}
}

func (s *Session) cancelLeaderInbox() *sessionLeaderInbox {
	s.mu.Lock()
	state := s.leaderInbox
	s.mu.Unlock()
	if state != nil {
		state.cancel()
	}
	return state
}
func (s *Session) joinLeaderInbox(ctx context.Context, state *sessionLeaderInbox) error {
	if state == nil {
		return nil
	}
	select {
	case <-state.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
