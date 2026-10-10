package cooperative

import "context"

// NativePromptHookModels is issued only at the canonical active Host boundary.
// It cannot admit Web sources or grant generic callback/frame privileges.
type NativePromptHookModels struct{ owner *Owner }

func NewNativePromptHookModels(scope *Scope, owner *Owner) (*NativePromptHookModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativePromptHookModels{owner: owner}, nil
}
func (a *NativePromptHookModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativePromptHookModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
