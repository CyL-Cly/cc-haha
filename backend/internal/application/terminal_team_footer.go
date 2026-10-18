package application

import "context"

func (s *Session) SubscribeTerminalTeamFooter(ctx context.Context, listener func(bool)) (bool, func(), error) {
	return s.process.SubscribeTerminalTeamFooter(ctx, listener)
}
