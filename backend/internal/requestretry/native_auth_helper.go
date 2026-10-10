package requestretry

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// NativeAuthHelper is minted by the actual shared Host for its current owner
// scope. It resolves the original helper headers and sync fallback key at the
// actual GetClient stage, rather than putting a prepared credential in input.
type NativeAuthHelperResult struct {
	Key, Token *string
	Configured bool
}
type NativeAuthHelper struct {
	owner   *cooperative.Owner
	scope   *cooperative.Scope
	resolve func(context.Context, bool) (NativeAuthHelperResult, error)
	clear   func() error
}

func NewNativeAuthHelper(owner *cooperative.Owner, scope *cooperative.Scope, resolve func(context.Context, bool) (NativeAuthHelperResult, error), clear func() error) (*NativeAuthHelper, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) || resolve == nil || clear == nil {
		return nil, cooperative.ErrRestricted
	}
	return &NativeAuthHelper{owner, scope, resolve, clear}, nil
}
func (b *NativeAuthHelper) Owns(owner *cooperative.Owner) bool {
	return b != nil && owner != nil && b.owner == owner && b.scope.BelongsTo(owner)
}
func (b *NativeAuthHelper) check(owner *cooperative.Owner, scope *cooperative.Scope) error {
	if b == nil || b.owner != owner || b.scope != scope || !b.Owns(owner) {
		return errors.New("native API key helper requires actual current owner scope")
	}
	return nil
}
func (b *NativeAuthHelper) Resolve(ctx context.Context, nonInteractive bool) (NativeAuthHelperResult, error) {
	if err := b.check(b.owner, b.scope); err != nil {
		return NativeAuthHelperResult{}, err
	}
	return b.resolve(ctx, nonInteractive)
}
func (b *NativeAuthHelper) Invalidate() error {
	if err := b.check(b.owner, b.scope); err != nil {
		return err
	}
	return b.clear()
}
