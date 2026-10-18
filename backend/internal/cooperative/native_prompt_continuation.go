package cooperative

import "context"

// NativePromptContinuations gives the actual prompt factory independent stacks
// on its existing Owner. It grants no model, ASK, or side-question authority.
// The factory additionally binds these stacks to its original user/settings
// graph before exposing its concrete Begin methods to the Host.
type NativePromptContinuations struct {
	owner   *Owner
	closing bool // protected by owner.mu; Close is administrative admission only
}

func NewNativePromptContinuations(scope *Scope, owner *Owner) (*NativePromptContinuations, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativePromptContinuations{owner: owner}, nil
}

func (p *NativePromptContinuations) Owns(owner *Owner) bool {
	return p != nil && owner != nil && p.owner == owner
}
func (p *NativePromptContinuations) Current(scope *Scope) bool {
	return p != nil && scope != nil && scope.BelongsTo(p.owner) && scope.sideContinuation != nil && scope.sideContinuation.owner == p.owner && scope.sideContinuation.promptAuthority == p && scope.sideContinuation.executing.Load()
}

// CheckCaller also guards a warm memo lookup before its resolver can change
// caches. Having an issuer from startup does not authorize an ordinary Read
// callback to start a new prompt producer later.
func (p *NativePromptContinuations) CheckCaller(caller *Scope) error {
	if p == nil || !caller.BelongsTo(p.owner) || caller.frame != 0 || caller.callback != 0 && !p.Current(caller) || caller.sideContinuation != nil && !p.Current(caller) {
		return ErrRestricted
	}
	if err := caller.check(); err != nil {
		return err
	}
	p.owner.mu.Lock()
	closed := p.closing || p.owner.closing
	p.owner.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return nil
}

// Start returns after the synchronous prefix has either completed or parked at
// its first real await. Only the Owner's private continuation command can give
// that stack the token for its next segment. Physical workers never get Scope.
func (p *NativePromptContinuations) Start(caller *Scope, ctx context.Context, produce func(*Scope) error, complete func(*Scope, error)) error {
	if ctx == nil || produce == nil || complete == nil {
		return ErrRestricted
	}
	if err := p.CheckCaller(caller); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	delegate := &NativeSideQuestionModels{owner: p.owner}
	lane, err := delegate.createContinuation(ctx, produce, complete, false)
	if err != nil {
		return err
	}
	lane.authority = nil
	lane.promptAuthority = p
	return lane.advance()
}

func (scope *Scope) IsNativePromptContinuation(owner *Owner) bool {
	return scope != nil && scope.BelongsTo(owner) && scope.sideContinuation != nil && scope.sideContinuation.promptAuthority.Current(scope)
}

func (p *NativePromptContinuations) BeginClose() {
	if p == nil {
		return
	}
	p.owner.mu.Lock()
	p.closing = true
	p.owner.mu.Unlock()
}

// Join observes the actual stacks, including already-started sibling producers
// after Promise.all rejected. It must be called outside the Owner.
func (p *NativePromptContinuations) Join(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return ErrRestricted
	}
	p.BeginClose()
	for {
		p.owner.mu.Lock()
		pending := false
		for lane := range p.owner.sideContinuations {
			if lane.promptAuthority == p {
				pending = true
				break
			}
		}
		changed := p.owner.sideChanged
		p.owner.mu.Unlock()
		if !pending {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
