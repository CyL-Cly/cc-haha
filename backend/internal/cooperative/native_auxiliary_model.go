package cooperative

import "context"

// NativeAuxiliaryModels is issued at the canonical native SDKHost construction
// boundary. It is not an input/read privilege or a generic callback escape.
// The Host additionally proves its actual Web handler bindings before Run.
type NativeAuxiliaryModels struct{ owner *Owner }

func NewNativeAuxiliaryModels(scope *Scope, owner *Owner) (*NativeAuxiliaryModels, error) {
	if !scope.BelongsTo(owner) || scope.callback != 0 || scope.frame != 0 || scope.sideContinuation != nil && scope.sideContinuation.promptAuthority != nil {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	owner.mu.Lock()
	active, closing := owner.active, owner.closing
	owner.mu.Unlock()
	if !active || closing || owner.life.Err() != nil {
		return nil, ErrRestricted
	}
	return &NativeAuxiliaryModels{owner: owner}, nil
}

// Run resumes an already admitted Web call's auxiliary producer on this Owner.
// The native callback itself keeps its Read restriction; only this new model
// stack can use the existing Frame/open/read/finally primitives. Model ctx is
// passed unchanged by the SDKHost. Owner lifetime remains alive for finally.
func (a *NativeAuxiliaryModels) Run(caller *Scope, ctx context.Context, source string, producer func(*Scope) error) error {
	if a == nil || source != "web_fetch_apply" && source != "web_search_tool" {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, func(admitted *Scope) error {
		return runParkedNativeAuxiliary(a.owner, admitted, ctx, producer, a, nil)
	})
}

func runNativeAuxiliaryProducer(owner *Owner, caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if !caller.BelongsTo(owner) || caller.frame != 0 || ctx == nil || producer == nil {
		return ErrRestricted
	}
	if caller.sideContinuation != nil && !caller.IsNativeSideQuestion(owner) && !caller.IsNativeAutoDream(owner) && !caller.isNativePermissionRequestHook(owner) {
		return ErrRestricted
	}
	if e := caller.check(); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	owner.mu.Lock()
	active, closing := owner.active, owner.closing
	owner.mu.Unlock()
	if closing || owner.life.Err() != nil {
		return ErrClosed
	}
	if !active && !caller.IsNativeSideQuestion(owner) && !caller.IsNativeAutoDream(owner) && !caller.isNativePermissionRequestHook(owner) {
		return ErrRestricted
	}
	scope := &Scope{owner: owner, ctx: caller.ctx, valid: true, sideContinuation: caller.sideContinuation}
	defer func() { scope.valid = false }()
	return producer(scope)
}
