package cooperative

import "context"

// NativeAutoModeModels admits only the canonical permission classifier producer.
// It does not grant another query source a callback or model privilege.
type NativeAutoModeModels struct{ owner *Owner }

func NewNativeAutoModeModels(scope *Scope, owner *Owner) (*NativeAutoModeModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeAutoModeModels{owner: owner}, nil
}
func (a *NativeAutoModeModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeAutoModeModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
