package cooperative

import "context"

// NativeAgentHookModels is minted only by the canonical active Host. It
// resumes a child query context on the same Owner; it grants no generic frame
// access to native callback scopes or other secondary-model purposes.
type NativeAgentHookModels struct{ owner *Owner }

func NewNativeAgentHookModels(scope *Scope, owner *Owner) (*NativeAgentHookModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeAgentHookModels{owner: owner}, nil
}
func (a *NativeAgentHookModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeAgentHookModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
