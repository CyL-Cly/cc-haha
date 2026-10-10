package cooperative

import "context"

// NativeCloudHTTP is the credential-cache producer's finite HTTP continuation.
// It is issued on the selected cloud's actual native owner, before the cache
// forks an asynchronous refresh. It cannot be minted by ordinary Read jobs.
type NativeCloudHTTP struct {
	owner   *Owner
	purpose string
}

func NewNativeCloudHTTP(scope *Scope, owner *Owner, purpose string) (*NativeCloudHTTP, error) {
	if purpose != "aws_credentials" && purpose != "gcp_credentials" {
		return nil, ErrRestricted
	}
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeCloudHTTP{owner, purpose}, nil
}
func (a *NativeCloudHTTP) Owns(owner *Owner, purpose string) bool {
	return a != nil && owner != nil && a.owner == owner && a.purpose == purpose
}
func (a *NativeCloudHTTP) Run(caller *Scope, ctx context.Context, purpose string, produce func(*Scope) error) error {
	if a == nil || caller == nil || !a.Owns(caller.owner, purpose) || !caller.BelongsTo(a.owner) || caller.frame != 0 || ctx == nil || produce == nil {
		return ErrRestricted
	}
	if err := caller.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	a.owner.mu.Lock()
	closing := a.owner.closing
	a.owner.mu.Unlock()
	if closing || a.owner.life.Err() != nil {
		return ErrClosed
	}
	// Original memoized auth refresh continues while the model becomes idle;
	// unlike model auxiliary calls, it is tied to the cache/Owner lifecycle,
	// not to the continued presence of an active ASK.
	life, cancel := context.WithCancelCause(caller.ctx)
	stop := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	defer stop()
	defer cancel(nil)
	scope := &Scope{owner: a.owner, ctx: life, valid: true}
	defer func() { scope.valid = false }()
	return produce(scope)
}
