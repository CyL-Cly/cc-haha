package cooperative

import "context"

// NativeInsightsModels carries only the Source usage-report model purpose.
// Issuance requires the same canonical active Host boundary as Web and hooks.
type NativeInsightsModels struct{ owner *Owner }

func NewNativeInsightsModels(scope *Scope, owner *Owner) (*NativeInsightsModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeInsightsModels{owner: owner}, nil
}
func (a *NativeInsightsModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeInsightsModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, func(admitted *Scope) error {
		return runParkedNativeAuxiliary(a.owner, admitted, ctx, producer, nil, a)
	})
}
