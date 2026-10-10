package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// WaitTranscriptRuntime is off-owner native liveness, not an ASK checkpoint,
// untracked-Record join, or process-wide provider/output/diagnostics drain.
func (h *Host) WaitTranscriptRuntime(ctx context.Context) error {
	if h == nil || h.pending == nil {
		return cooperative.ErrRestricted
	}
	return h.pending.WaitRuntimeNative(ctx)
}
