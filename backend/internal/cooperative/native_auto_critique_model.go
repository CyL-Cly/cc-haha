package cooperative

import "context"

// This authority is minted by the public auto-mode critique command on its
// actual MainCommand owner. It admits that side query, never an ASK or tool.
type NativeAutoCritiqueModels struct{ owner *Owner }

func NewNativeAutoCritiqueModels(scope *Scope, owner *Owner) (*NativeAutoCritiqueModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeAutoCritiqueModels{owner: owner}, nil
}
func (a *NativeAutoCritiqueModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeAutoCritiqueModels) Run(caller *Scope, ctx context.Context, work func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, work)
}
