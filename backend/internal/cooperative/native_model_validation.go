package cooperative

import "context"

// NativeModelValidationModels admits only the canonical typed /model producer.
// It does not grant another query source a callback or model privilege.
type NativeModelValidationModels struct{ owner *Owner }

func NewNativeModelValidationModels(scope *Scope, owner *Owner) (*NativeModelValidationModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeModelValidationModels{owner: owner}, nil
}
func (a *NativeModelValidationModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeModelValidationModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
