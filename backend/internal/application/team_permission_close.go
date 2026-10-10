package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// The real parent registry also owns permissions borrowed by independent
// workers. Closing the parent rejects those futures before joining workers;
// cancelling the foreground model alone cannot release an idle child's ask.
func (s *Session) cancelSessionPermissions(ctx context.Context) error {
	s.mu.Lock()
	pending := make([]*pendingPermission, 0, len(s.pending))
	for _, request := range s.pending {
		pending = append(pending, request)
	}
	s.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Cancel, func(scope *cooperative.Scope) error {
		for _, request := range pending {
			_ = request.future.Reject(scope, context.Canceled)
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
