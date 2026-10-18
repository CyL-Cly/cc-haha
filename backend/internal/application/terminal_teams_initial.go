package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) SubscribeTerminalCachedTeamsFrames(ctx context.Context, listener func(tui.TerminalCachedTeamsFrame)) (tui.TerminalCachedTeamsFrame, func(), error) {
	initial, remove, err := s.process.SubscribeNativeTerminalTeamsInitial(ctx, func(value *clibootstrap.NativeTerminalTeamsInitial) {
		if listener != nil {
			if value == nil {
				listener(nil)
			} else {
				listener(value)
			}
		}
	})
	if err != nil || initial == nil {
		return nil, remove, err
	}
	return initial, remove, nil
}
