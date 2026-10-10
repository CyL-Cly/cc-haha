package cooperative

import "context"

// NativeImageHTTP is issued only by the canonical bootstrap before tool
// callbacks. It allows that actual image producer to continue fetch and body
// consumption on its original owner. Ordinary read callbacks cannot acquire
// this purpose or use its finite image-operation names as a generic escape.
type NativeImageHTTP struct{ owner *Owner }

func NewNativeImageHTTP(scope *Scope, owner *Owner) (*NativeImageHTTP, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeImageHTTP{owner}, nil
}
func (a *NativeImageHTTP) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeImageHTTP) Run(caller *Scope, ctx context.Context, operation string, produce func(*Scope) error) error {
	if a == nil || operation != "image_generate" && operation != "image_edit" {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, produce)
}
