package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) InvokeDoctor(ctx context.Context, emit tui.Emit) error {
	text, e := s.process.NativeDoctorCommand(ctx)
	if e != nil {
		return e
	}
	if emit == nil {
		return nil
	}
	return emit(tui.Event{Kind: tui.DoctorOpened, Text: text})
}
