package cooperative

// NativeFastModeRequest binds the current request to the actual process fast
// runtime. A serialized flag cannot construct this owner/scope authority.
type NativeFastModeRequest struct {
	owner   *Owner
	scope   *Scope
	resolve func(string, *bool) (bool, bool, error)
}

func NewNativeFastModeRequest(owner *Owner, scope *Scope, resolve func(string, *bool) (bool, bool, error)) (*NativeFastModeRequest, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) || resolve == nil {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	if scope.sideContinuation != nil && !scope.IsNativeSideQuestion(owner) && !scope.isNativePermissionRequestHook(owner) {
		return nil, ErrRestricted
	}
	return &NativeFastModeRequest{owner, scope, resolve}, nil
}
func (p *NativeFastModeRequest) Current() bool {
	return p != nil && p.scope.BelongsTo(p.owner) && p.owner.life.Err() == nil && (p.scope.sideContinuation == nil || p.scope.IsNativeSideQuestion(p.owner) || p.scope.isNativePermissionRequestHook(p.owner))
}
func (p *NativeFastModeRequest) Owns(owner *Owner) bool {
	return p != nil && p.owner == owner && p.Current()
}
func (p *NativeFastModeRequest) Resolve(model string, requested *bool) (bool, bool, error) {
	if !p.Current() {
		return false, false, ErrRestricted
	}
	return p.resolve(model, requested)
}
