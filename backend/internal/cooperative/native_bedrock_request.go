package cooperative

// NativeBedrockRequest is the sealed proof for one actual current model ASK's
// selected Bedrock client factory. A JSON input or environment cannot create it.
// It grants no callback/HTTP/model privilege by itself.
type NativeBedrockRequest struct {
	owner *Owner
	scope *Scope
}

func NewNativeBedrockRequest(owner *Owner, scope *Scope) (*NativeBedrockRequest, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	return &NativeBedrockRequest{owner, scope}, nil
}
func (p *NativeBedrockRequest) Current() bool {
	return p != nil && p.scope.BelongsTo(p.owner) && p.owner.life.Err() == nil
}
func (p *NativeBedrockRequest) Owns(owner *Owner, scope *Scope) bool {
	return p != nil && p.owner == owner && p.scope == scope && p.Current()
}
