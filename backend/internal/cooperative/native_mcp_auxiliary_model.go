package cooperative

import "context"

// NativeMCPAuxiliaryModels is minted at the real non-ASK MCP host constructor.
// It admits only the two original Web producers on that host's owner lifetime.
// Generic read callbacks retain their Frame/open/turn restrictions.
type NativeMCPAuxiliaryModels struct{ owner *Owner }

func NewNativeMCPAuxiliaryModels(scope *Scope, owner *Owner) (*NativeMCPAuxiliaryModels, error) {
	if _, e := NewNativeAuxiliaryModels(scope, owner); e != nil {
		return nil, e
	}
	return &NativeMCPAuxiliaryModels{owner}, nil
}
func (a *NativeMCPAuxiliaryModels) Run(caller *Scope, ctx context.Context, source string, produce func(*Scope) error) error {
	if a == nil || (source != "web_fetch_apply" && source != "web_search_tool") || !caller.BelongsTo(a.owner) || caller.frame != 0 || ctx == nil || produce == nil {
		return ErrRestricted
	}
	if e := caller.check(); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return context.Cause(ctx)
	}
	a.owner.mu.Lock()
	closing := a.owner.closing
	a.owner.mu.Unlock()
	if closing || a.owner.life.Err() != nil {
		return ErrClosed
	}
	scope := &Scope{owner: a.owner, ctx: caller.ctx, valid: true}
	defer func() { scope.valid = false }()
	return produce(scope)
}
