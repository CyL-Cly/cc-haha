package analytics

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Abort is the ordinary subcommand's direct process.exit boundary. Unlike the
// interactive graceful shutdown it does not begin an exporter flush or wait
// 500ms. Already-started physical work is canceled and joined by its owner.
func (s *Service) Abort(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	s.closed = true
	s.queue = nil
	for _, timer := range []*cooperative.TimerTicket{s.timer, s.retry, s.batchTimer} {
		if timer != nil {
			_ = scope.ClearTimer(timer)
		}
	}
	s.timer, s.retry, s.batchTimer = nil, nil, nil
	s.cancel()
	return nil
}
