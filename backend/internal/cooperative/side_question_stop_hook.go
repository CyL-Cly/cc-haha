package cooperative

import "context"

// SubmitNativeSideQuestionStopHook is the original SubagentStop async job
// derived from an already issued side fork. A generic callback cannot acquire
// the tag: without the private continuation this returns unhandled, leaving
// its existing ordinary callback path and guards unchanged.
func (scope *Scope) SubmitNativeSideQuestionStopHook(ctx context.Context, owner *Owner, event string, run func(*Scope) error) (*Receipt, bool, error) {
	if scope == nil || scope.sideContinuation == nil || scope.sideContinuation.authority == nil {
		return nil, false, nil
	}
	if event != "SubagentStop" || !scope.BelongsTo(owner) || scope.sideContinuation.owner != owner || !scope.sideContinuation.executing.Load() || run == nil {
		return nil, true, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, true, err
	}
	authority := scope.sideContinuation.authority
	lane, err := authority.createContinuation(ctx, func(child *Scope) error { return child.Inline(Read, run) }, func(*Scope, error) {}, true)
	if err != nil {
		return nil, true, err
	}
	// Register before enqueue: the parent may finish before this hook's first
	// segment, but Join must still own and drain this exact queued job.
	receipt := &Receipt{done: make(chan struct{})}
	owner.mu.Lock()
	owner.queue = append(owner.queue, command{class: Read, receipt: receipt, independent: true, sideContinuation: lane})
	select {
	case owner.wake <- struct{}{}:
	default:
	}
	owner.mu.Unlock()
	return receipt, true, nil
}
