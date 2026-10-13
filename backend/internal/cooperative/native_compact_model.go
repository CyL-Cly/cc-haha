package cooperative

import "context"

// NativeCompactModels admits only the canonical summary producer. It shares
// the current owner and cancellation context, without granting a new tool call.
type NativeCompactModels struct{ owner *Owner }

func NewNativeCompactModels(scope *Scope, owner *Owner) (*NativeCompactModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeCompactModels{owner: owner}, nil
}
func (a *NativeCompactModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeCompactModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
