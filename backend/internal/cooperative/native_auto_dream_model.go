package cooperative

import "context"

// NativeAutoDreamModels owns the original skipTranscript cache-safe fork. It
// shares the native continuation machinery, but carries a distinct current
// segment seal: neither a side question nor an arbitrary input can open it.
type NativeAutoDreamModels struct{ delegate *NativeSideQuestionModels }

func NewNativeAutoDreamModels(scope *Scope, owner *Owner) (*NativeAutoDreamModels, error) {
	delegate, err := NewNativeSideQuestionModels(scope, owner)
	if err != nil {
		return nil, err
	}
	return &NativeAutoDreamModels{delegate}, nil
}
func (p *NativeAutoDreamModels) Owns(owner *Owner) bool { return p != nil && p.delegate.Owns(owner) }
func (p *NativeAutoDreamModels) Current(scope *Scope) bool {
	return p != nil && p.delegate.Current(scope) && scope.sideContinuation.dreamAuthority == p
}
func (scope *Scope) IsNativeAutoDream(owner *Owner) bool {
	return scope.BelongsTo(owner) && scope.sideContinuation != nil && scope.sideContinuation.dreamAuthority.Current(scope)
}
func (p *NativeAutoDreamModels) Start(caller *Scope, ctx context.Context, produce func(*Scope) error, complete func(*Scope, error)) error {
	if p == nil || !caller.BelongsTo(p.delegate.owner) || caller.frame != 0 || caller.sideContinuation != nil || ctx == nil || produce == nil || complete == nil {
		return ErrRestricted
	}
	if err := caller.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	lane, err := p.delegate.createContinuation(ctx, produce, complete, false)
	if err != nil {
		return err
	}
	lane.dreamAuthority = p
	return lane.advance()
}
func (p *NativeAutoDreamModels) Join(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.delegate.Join(ctx)
}
