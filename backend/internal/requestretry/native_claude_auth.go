package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type NativeClaudeAuth struct {
	owner      *cooperative.Owner
	scope      *cooperative.Scope
	proof      *cooperative.NativeClaudeRequest
	subscriber func(context.Context) (bool, error)
	token      func(context.Context) (*string, error)
}

func NewNativeClaudeAuth(owner *cooperative.Owner, scope *cooperative.Scope, subscriber func(context.Context) (bool, error), token func(context.Context) (*string, error)) (*NativeClaudeAuth, error) {
	if subscriber == nil || token == nil {
		return nil, cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeClaudeRequest(owner, scope)
	if err != nil {
		return nil, err
	}
	return &NativeClaudeAuth{owner, scope, proof, subscriber, token}, nil
}
func (b *NativeClaudeAuth) Owns(owner *cooperative.Owner, scope *cooperative.Scope) bool {
	return b != nil && b.owner == owner && b.scope == scope && b.proof.Owns(owner, scope)
}
func (b *NativeClaudeAuth) Token(ctx context.Context) (*string, error) {
	if !b.Owns(b.owner, b.scope) {
		return nil, cooperative.ErrRestricted
	}
	return b.token(ctx)
}
func BindNativeClaudeHost(ctx context.Context, input *Input, deps Dependencies) error {
	if deps.NativeClaudeError != nil {
		return deps.NativeClaudeError
	}
	if deps.NativeClaudeAuth == nil {
		return nil
	}
	if input == nil || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || !deps.NativeClaudeAuth.Owns(deps.StreamOwner, deps.StreamScope) {
		return cooperative.ErrRestricted
	}
	subscriber, err := deps.NativeClaudeAuth.subscriber(ctx)
	if err != nil {
		return err
	}
	input.Context.Host.NativeClaudeSubscriber = subscriber
	input.Context.Host.NativeClaudeProof = deps.NativeClaudeAuth.proof
	return nil
}

func (b *NativeClaudeAuth) Proof(owner *cooperative.Owner, scope *cooperative.Scope) *cooperative.NativeClaudeRequest {
	if !b.Owns(owner, scope) {
		return nil
	}
	return b.proof
}
