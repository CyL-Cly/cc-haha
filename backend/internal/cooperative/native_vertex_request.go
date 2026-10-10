package cooperative

// NativeVertexRequest is the sealed proof for one actual current model ASK's
// selected Vertex client factory. A JSON input or environment cannot create it.
// It grants no callback/HTTP/model privilege by itself.
type NativeVertexRequest struct {
	owner *Owner
	scope *Scope
}

func NewNativeVertexRequest(owner *Owner, scope *Scope) (*NativeVertexRequest, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) {
		return nil, ErrRestricted
	}
	if err := scope.check(); err != nil {
		return nil, err
	}
	return &NativeVertexRequest{owner, scope}, nil
}
func (p *NativeVertexRequest) Current() bool {
	return p != nil && p.scope.BelongsTo(p.owner) && p.owner.life.Err() == nil
}
func (p *NativeVertexRequest) Owns(owner *Owner, scope *Scope) bool {
	return p != nil && p.owner == owner && p.scope == scope && p.Current()
}
