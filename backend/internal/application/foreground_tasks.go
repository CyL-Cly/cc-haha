package application

import "context"

func (s *Session) SubscribeForegroundTasks(ctx context.Context, callback func(bool)) (bool, func(), error) {
	return s.process.SubscribeForegroundTasks(ctx, callback)
}
