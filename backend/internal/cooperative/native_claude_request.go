package cooperative

// NativeClaudeRequest proves that a current ASK owns the actual Claude auth
// consumer. A JSON credential or provider setting cannot construct this proof.
type NativeClaudeRequest struct {
	owner *Owner
	scope *Scope
}

func NewNativeClaudeRequest(owner *Owner, scope *Scope) (*NativeClaudeRequest, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	return &NativeClaudeRequest{owner, scope}, nil
}
func (p *NativeClaudeRequest) Current() bool {
	return p != nil && p.scope.BelongsTo(p.owner) && p.owner.life.Err() == nil
}
func (p *NativeClaudeRequest) Owns(owner *Owner, scope *Scope) bool {
	return p != nil && p.owner == owner && p.scope == scope && p.Current()
}
