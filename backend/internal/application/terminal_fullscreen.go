package application

import "context"

func (s *Session) ReadTerminalFullscreen(ctx context.Context) (bool, error) {
	return s.process.ReadTerminalFullscreen(ctx)
}

func (s *Session) SubscribeTerminalFullscreen(ctx context.Context, changed func()) (bool, func(), error) {
	return s.process.SubscribeNativeTerminalFullscreen(ctx, changed)
}
