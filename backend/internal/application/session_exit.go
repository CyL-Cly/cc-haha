package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// CloseWithReason keeps the ordinary shared Session lifecycle. Repeated exits
// wait for the first close's same result, including permission/tool drainage.
func (s *Session) CloseWithReason(ctx context.Context, reason string) error {
	return s.closeWithReason(ctx, reason)
}

var _ tui.ExitReasonSession = (*Session)(nil)
