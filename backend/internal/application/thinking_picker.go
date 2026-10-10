package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"time"
)

func (s *Session) InvokeThinkingPicker(ctx context.Context, emit tui.Emit) error {
	if emit == nil {
		emit = func(tui.Event) error { return nil }
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	if s.thinkingPicker != "" {
		s.thinkingPicker = ""
		s.mu.Unlock()
		return emit(tui.Event{Kind: tui.ThinkingPickerClosed})
	}
	s.mu.Unlock()
	thinking, mid, e := s.process.TerminalThinking(ctx)
	if e != nil {
		return e
	}
	props := tui.ThinkingPicker{ID: newID(), Current: thinking.Type != "disabled", MidConversation: mid}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	s.thinkingPicker = props.ID
	s.mu.Unlock()
	return emit(tui.Event{Kind: tui.ThinkingPickerOpened, ThinkingPicker: &props})
}
func (s *Session) RespondThinkingPicker(ctx context.Context, id string, result tui.ThinkingPickerResult, emit tui.Emit) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	if id == "" || s.thinkingPicker != id {
		s.mu.Unlock()
		return &tui.UnsupportedAction{Action: "stale thinking picker callback"}
	}
	s.thinkingPicker = ""
	s.mu.Unlock()
	if result.Cancelled {
		return nil
	}
	if e := s.process.SetTerminalThinking(ctx, result.Enabled); e != nil {
		return e
	}
	if emit != nil {
		text := "Thinking off"
		if result.Enabled {
			text = "Thinking on"
		}
		return emit(tui.Event{Kind: tui.Notice, Text: text, NoticeTimeout: 3 * time.Second})
	}
	return nil
}
