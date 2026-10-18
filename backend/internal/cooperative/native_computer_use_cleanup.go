package cooperative

import "context"

// NativeComputerUseCleanup is minted by the actual native SDKHost constructor.
// It retains only that owner's cleanup lifetime, never a model/Frame authority.
type NativeComputerUseCleanup struct {
	owner  *Owner
	life   context.Context
	cancel context.CancelCauseFunc
}

func NewNativeComputerUseCleanup(scope *Scope, owner *Owner) (*NativeComputerUseCleanup, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancelCause(owner.life)
	return &NativeComputerUseCleanup{owner: owner, life: life, cancel: cancel}, nil
}

// BeginClose runs before the host waits for its current ASK/native references.
// Cancelling only the model cannot stop Source's independent cleanup signal.
func (cleanup *NativeComputerUseCleanup) BeginClose() {
	if cleanup != nil {
		cleanup.cancel(ErrClosed)
	}
}

// Run preserves Source's independent turn-end cleanup signal after model abort.
// Global close still cancels this scope. The callback restriction prevents the
// cleanup stack from issuing a provider Frame or a new ASK.
func (cleanup *NativeComputerUseCleanup) Run(caller *Scope, work func(*Scope, context.Context) error) error {
	if cleanup == nil || !caller.BelongsTo(cleanup.owner) || caller.frame != 0 || work == nil {
		return ErrRestricted
	}
	cleanup.owner.mu.Lock()
	active, closing := cleanup.owner.active, cleanup.owner.closing
	cleanup.owner.mu.Unlock()
	if closing || cleanup.life.Err() != nil {
		return ErrClosed
	}
	if !active {
		return ErrRestricted
	}
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(caller.ctx))
	stop := context.AfterFunc(cleanup.life, func() { cancel(context.Cause(cleanup.life)) })
	defer stop()
	defer cancel(nil)
	scope := &Scope{owner: cleanup.owner, ctx: ctx, valid: true, callback: 1}
	defer func() { scope.valid = false }()
	return work(scope, ctx)
}
