package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Both queryloop abort stages delegate through the same main-thread resource
// consumer. The Context carries the owning NativeMCP.EndTurnOwner closure;
// calling it separately here would await and release the same turn twice.
func (h *Host) abortCleanupHandler(contextForRound func(queryloop.Round) queryhooks.Context) func(context.Context, queryloop.Round) error {
	return func(ctx context.Context, round queryloop.Round) error {
		return h.hooks.AbortCleanup(ctx, contextForRound(round))
	}
}
