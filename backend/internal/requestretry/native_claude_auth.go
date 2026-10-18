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
	check      func(context.Context) error
}

func NewNativeClaudeAuth(owner *cooperative.Owner, scope *cooperative.Scope, subscriber func(context.Context) (bool, error), token func(context.Context) (*string, error), check ...func(context.Context) error) (*NativeClaudeAuth, error) {
	if subscriber == nil || token == nil || len(check) > 1 || len(check) == 1 && check[0] == nil {
		return nil, cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeClaudeRequest(owner, scope)
	if err != nil {
		return nil, err
	}
	binding := &NativeClaudeAuth{owner: owner, scope: scope, proof: proof, subscriber: subscriber, token: token}
	if len(check) == 1 {
		binding.check = check[0]
	}
	return binding, nil
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

// Check is the signal-free getAnthropicClient credential staleness producer,
// before choosing any Anthropic/OpenAI/Grok authentication transport.
func (b *NativeClaudeAuth) Check(ctx context.Context) error {
	if b == nil || !b.Owns(b.owner, b.scope) {
		return cooperative.ErrRestricted
	}
	if b.check == nil {
		return nil
	}
	return b.check(ctx)
}

func (b *NativeClaudeAuth) Subscriber(ctx context.Context) (bool, error) {
	if b == nil || !b.Owns(b.owner, b.scope) {
		return false, cooperative.ErrRestricted
	}
	return b.subscriber(ctx)
}
func BindNativeClaudeHost(ctx context.Context, input *Input, deps Dependencies) error {
	if deps.NativeClaudeError != nil {
		return deps.NativeClaudeError
	}
	if deps.NativeThinkingSummaryBeta != nil {
		if input == nil || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || !deps.StreamScope.BelongsTo(deps.StreamOwner) {
			return cooperative.ErrRestricted
		}
		value, err := deps.NativeThinkingSummaryBeta(deps.StreamScope, input.Context.Options.Model)
		if err != nil {
			return err
		}
		input.Context.Host.NativeThinkingSummaryBeta = value
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
