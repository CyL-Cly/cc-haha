package queryhooks

import "context"

// AbortCleanup consumes the owning host's native Computer Use turn cleanup.
// Source's streaming/tool abort paths await it outside the aborted model signal,
// swallow native cleanup failures, and exclude children from the main CU lease.
func (service *Service) AbortCleanup(ctx context.Context, call Context) error {
	if ctx == nil {
		return unsupported("abort cleanup requires original caller context")
	}
	if call.AgentID != "" {
		return nil
	}
	if !call.ComputerUseInactive {
		if call.CleanupComputerUse == nil {
			return unsupported("abort computer-use overlay/badge/hidden-app/lock lifecycle")
		}
		_ = call.CleanupComputerUse(context.WithoutCancel(ctx))
	}
	return nil
}
