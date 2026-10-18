package cooperative

import "context"

// NativeRemoteTitleModels binds the canonical Host's no-tools queryHaiku
// producers for CCR title/branch generation, generated session titles, and automatic conversation naming.
// An arbitrary querySource string cannot mint transport authority.
type NativeRemoteTitleModels struct{ owner *Owner }

func NewNativeRemoteTitleModels(scope *Scope, owner *Owner) (*NativeRemoteTitleModels, error) {
	if _, err := NewNativeAuxiliaryModels(scope, owner); err != nil {
		return nil, err
	}
	return &NativeRemoteTitleModels{owner: owner}, nil
}
func (a *NativeRemoteTitleModels) Owns(owner *Owner) bool {
	return a != nil && owner != nil && a.owner == owner
}
func (a *NativeRemoteTitleModels) AcceptsPurpose(purpose string) bool {
	return a != nil && (purpose == "teleport_generate_title" || purpose == "rename_generate_name" || purpose == "generate_session_title")
}
func (a *NativeRemoteTitleModels) Run(caller *Scope, ctx context.Context, producer func(*Scope) error) error {
	if a == nil {
		return ErrRestricted
	}
	return runNativeAuxiliaryProducer(a.owner, caller, ctx, producer)
}
