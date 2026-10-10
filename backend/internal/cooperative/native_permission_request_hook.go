package cooperative

import "context"

// NativePermissionRequestHooks is issued by the actual native SDK Host. Its
// stacks share the Owner token scheduler, but cannot claim a side-question
// identity or admit another ASK. The original hook and model producers remain
// on their own stacks through their real awaits and finally blocks.
type NativePermissionRequestHooks struct {
	owner       *Owner
	life        context.Context
	cancel      context.CancelFunc
	closing     bool
	turnCancels []context.CancelFunc
}

func NewNativePermissionRequestHooks(scope *Scope, owner *Owner) (*NativePermissionRequestHooks, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(owner.life)
	return &NativePermissionRequestHooks{owner: owner, life: life, cancel: cancel}, nil
}
func (p *NativePermissionRequestHooks) Owns(owner *Owner) bool { return p != nil && p.owner == owner }
func (p *NativePermissionRequestHooks) Current(scope *Scope) bool {
	return p != nil && scope.BelongsTo(p.owner) && scope.sideContinuation != nil && scope.sideContinuation.permissionAuthority == p && scope.sideContinuation.executing.Load()
}
func (p *NativePermissionRequestHooks) Start(caller *Scope, ctx context.Context, produce func(*Scope, context.Context) error, complete func(*Scope, error)) error {
	if p == nil || !caller.BelongsTo(p.owner) || caller.frame != 0 || ctx == nil || produce == nil || complete == nil {
		return ErrRestricted
	}
	if err := caller.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	active, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.life, cancel)
	lane, err := p.createContinuation(func(scope *Scope) error { return produce(scope, active) }, func(scope *Scope, err error) { stop(); cancel(); complete(scope, err) }, false)
	if err != nil {
		stop()
		cancel()
		return err
	}
	return lane.advance()
}
func (p *NativePermissionRequestHooks) createContinuation(produce func(*Scope) error, complete func(*Scope, error), descendant bool) (*sideQuestionContinuation, error) {
	owner := p.owner
	owner.mu.Lock()
	if owner.closing || p.closing && !descendant {
		owner.mu.Unlock()
		return nil, ErrClosed
	}
	lane := &sideQuestionContinuation{permissionAuthority: p, owner: owner, resume: make(chan struct{}), segment: make(chan bool), done: make(chan struct{})}
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

// SubmitNativePermissionRequestHook derives only an original PermissionRequest
// executor job. It registers before enqueue so root verdict/SDK completion can
// never erase the real late hook or allow Join to miss a queued descendant.
func (scope *Scope) SubmitNativePermissionRequestHook(owner *Owner, event string, run func(*Scope) error) (*Receipt, bool, error) {
	if scope == nil || scope.sideContinuation == nil || scope.sideContinuation.permissionAuthority == nil {
		return nil, false, nil
	}
	p := scope.sideContinuation.permissionAuthority
	if event != "PermissionRequest" || !p.Current(scope) || p.owner != owner || run == nil {
		return nil, true, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, true, err
	}
	lane, err := p.createContinuation(func(child *Scope) error { return child.Inline(Read, run) }, func(*Scope, error) {}, true)
	if err != nil {
		return nil, true, err
	}
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
func (p *NativePermissionRequestHooks) Cancel(scope *Scope) error {
	if p == nil {
		return nil
	}
	if !scope.BelongsTo(p.owner) {
		return ErrRestricted
	}
	p.owner.mu.Lock()
	p.closing = true
	p.owner.mu.Unlock()
	p.cancel()
	return nil
}
func (p *NativePermissionRequestHooks) Join(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return ErrRestricted
	}
	for {
		p.owner.mu.Lock()
		p.closing = true
		pending := false
		for lane := range p.owner.sideContinuations {
			if lane.permissionAuthority == p {
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

// This kernel-only predicate checks the exact currently executing permission
// token. A parked scope, side lane or foreign authority is never a proof.
func (scope *Scope) isNativePermissionRequestHook(owner *Owner) bool {
	return scope.BelongsTo(owner) && scope.sideContinuation != nil && scope.sideContinuation.owner == owner && scope.sideContinuation.permissionAuthority != nil && scope.sideContinuation.permissionAuthority.Current(scope)
}

// Normal tool/turn completion does not abort Source's original hook signal.
// Preserve admitted late hooks through their finally; explicit interrupt and
// administrative Close still call the real cancellation function immediately.
func (p *NativePermissionRequestHooks) RetainTurnCancel(scope *Scope, cancel context.CancelFunc) bool {
	if p == nil || !scope.BelongsTo(p.owner) || cancel == nil {
		return false
	}
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	for lane := range p.owner.sideContinuations {
		if lane.permissionAuthority == p {
			p.turnCancels = append(p.turnCancels, cancel)
			return true
		}
	}
	return false
}
func (p *NativePermissionRequestHooks) finishTurnCancels() {
	owner := p.owner
	owner.mu.Lock()
	for lane := range owner.sideContinuations {
		if lane.permissionAuthority == p {
			owner.mu.Unlock()
			return
		}
	}
	cancels := p.turnCancels
	p.turnCancels = nil
	owner.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}
