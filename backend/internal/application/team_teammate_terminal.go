package application

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type teammateEventSink struct{ emit tui.Emit }

func (s *Session) SubscribeTeammateEvents(emit tui.Emit) func() {
	sink := &teammateEventSink{emit: emit}
	s.mu.Lock()
	if !s.closed {
		s.teammateEventSink = sink
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.teammateEventSink == sink {
				s.teammateEventSink = nil
			}
			s.mu.Unlock()
		})
	}
}

func (s *Session) SubscribeTeammates(ctx context.Context, callback func(protocol.JSONObject)) (protocol.JSONObject, func(), error) {
	return s.process.SubscribeTeammateTasks(ctx, callback)
}
func (s *Session) ControlTeammate(ctx context.Context, action tui.TeammateAction) error {
	switch action.Kind {
	case "stop-dream":
		return s.process.StopNativeAutoDream(ctx, action.TaskID)
	case "enter":
		return s.process.SetTeammateView(ctx, action.TaskID)
	case "exit":
		return s.process.SetTeammateView(ctx, "")
	case "interrupt":
		_, err := s.process.InterruptTeammateWork(ctx, action.TaskID)
		return err
	case "kill":
		_, err := s.process.KillTeammate(ctx, action.TaskID)
		return err
	case "send":
		return s.process.SendTeammateUserMessage(ctx, action.TaskID, action.Message)
	default:
		return &tui.UnsupportedAction{Action: action.Kind}
	}
}
