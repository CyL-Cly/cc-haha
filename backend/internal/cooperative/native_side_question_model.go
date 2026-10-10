package cooperative

import "context"

// NativeSideQuestionModels is issued at the canonical Host constructor only.
// Its continuation is a second call stack on the same Owner, never another
// Owner, ASK flag or privilege for generic Read/Input callbacks.
type NativeSideQuestionModels struct {
	owner   *Owner
	closing bool
}

func NewNativeSideQuestionModels(scope *Scope, owner *Owner) (*NativeSideQuestionModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeSideQuestionModels{owner: owner}, nil
}
func (p *NativeSideQuestionModels) Owns(owner *Owner) bool {
	return p != nil && owner != nil && p.owner == owner
}
func (p *NativeSideQuestionModels) Current(scope *Scope) bool {
	return p != nil && scope.BelongsTo(p.owner) && scope.sideContinuation != nil && scope.sideContinuation.authority == p && scope.sideContinuation.executing.Load()
}

// Start runs until the first actual await, then returns the Owner to its caller.
// Only the Owner's private resume commands can run the next segment. A producer
// goroutine owns the stack, but cannot execute a segment concurrently with the
// Owner: every await/return hands back its exclusive token before dispatch.
func (p *NativeSideQuestionModels) Start(caller *Scope, ctx context.Context, produce func(*Scope) error, complete func(*Scope, error)) error {
	if p == nil || !caller.BelongsTo(p.owner) || caller.frame != 0 || ctx == nil || produce == nil || complete == nil {
		return ErrRestricted
	}
	if err := caller.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	lane, err := p.createContinuation(ctx, produce, complete, false)
	if err != nil {
		return err
	}
	return lane.advance()
}

// IsNativeSideQuestion is a read-only current-segment proof for Source callers
// which must project child identity. A retained parked/finished scope is false.
func (scope *Scope) IsNativeSideQuestion(owner *Owner) bool {
	return scope.BelongsTo(owner) && scope.sideContinuation != nil && scope.sideContinuation.owner == owner && scope.sideContinuation.authority != nil && scope.sideContinuation.executing.Load()
}

func (p *NativeSideQuestionModels) createContinuation(ctx context.Context, produce func(*Scope) error, complete func(*Scope, error), descendant bool) (*sideQuestionContinuation, error) {
	owner := p.owner
	owner.mu.Lock()
	if owner.closing || p.closing && !descendant {
		owner.mu.Unlock()
		return nil, ErrClosed
	}
	lane := &sideQuestionContinuation{authority: p, owner: owner, resume: make(chan struct{}), segment: make(chan bool), done: make(chan struct{})}
	if owner.sideContinuations == nil {
		owner.sideContinuations = map[*sideQuestionContinuation]bool{}
	}
	owner.sideContinuations[lane] = true
	owner.sideChangedLocked()
	owner.mu.Unlock()
	scope := &Scope{owner: owner, ctx: owner.life, valid: true, sideContinuation: lane}
	go func() {
		<-lane.resume
		err := produce(scope)
		complete(scope, err)
		scope.valid = false
		lane.segment <- true
		close(lane.done)
	}()
	return lane, nil
}
func (owner *Owner) sideChangedLocked() {
	if owner.sideChanged != nil {
		close(owner.sideChanged)
	}
	owner.sideChanged = make(chan struct{})
}

// Join closes external side admission and waits for actual root/descendant
// stacks, including hook jobs queued before their first segment. The caller's
// deadline controls waiting only; it never releases native resources early.
func (p *NativeSideQuestionModels) Join(ctx context.Context) error {
	if p == nil {
		return nil
	}
	for {
		p.owner.mu.Lock()
		p.closing = true
		pending := false
		for lane := range p.owner.sideContinuations {
			if lane.authority == p {
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
