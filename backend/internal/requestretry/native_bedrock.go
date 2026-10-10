package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type NativeBedrockClient struct {
	BaseURL     string
	Config      *provider.BedrockConfig
	BearerToken string
}

// NativeBedrock is minted by the actual process's shared cloud producer for
// the current ASK. It carries no prepared request or caller-supplied credential.
type NativeBedrock struct {
	owner     *cooperative.Owner
	scope     *cooperative.Scope
	client    func(context.Context, string) (NativeBedrockClient, error)
	canonical func(string) (string, error)
	proof     *cooperative.NativeBedrockRequest
}

func NewNativeBedrock(owner *cooperative.Owner, scope *cooperative.Scope, client func(context.Context, string) (NativeBedrockClient, error), canonical ...func(string) (string, error)) (*NativeBedrock, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) || client == nil {
		return nil, cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeBedrockRequest(owner, scope)
	if err != nil {
		return nil, err
	}
	value := &NativeBedrock{owner: owner, scope: scope, client: client, proof: proof}
	if len(canonical) > 1 {
		return nil, cooperative.ErrRestricted
	}
	if len(canonical) == 1 {
		value.canonical = canonical[0]
	}
	return value, nil
}
func (b *NativeBedrock) Canonical(model string) (string, error) {
	if !b.Owns(b.owner, b.scope) || b.canonical == nil {
		return "", cooperative.ErrRestricted
	}
	return b.canonical(model)
}

func BindNativeBedrockHost(input *Input, deps Dependencies) error {
	if deps.NativeCloudError != nil {
		return deps.NativeCloudError
	}
	if deps.NativeBedrock == nil {
		return nil
	}
	if input == nil || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || !deps.NativeBedrock.Owns(deps.StreamOwner, deps.StreamScope) {
		return cooperative.ErrRestricted
	}
	if openAIEnvTruthy(input.Context.Host.Env["CLAUDE_CODE_USE_BEDROCK"]) {
		input.Context.Host.NativeBedrockBound = true
		input.Context.Host.NativeBedrockProof = deps.NativeBedrock.proof
		input.Context.Host.NativeBedrockCanonical = deps.NativeBedrock.Canonical
	}
	return nil
}
func (b *NativeBedrock) Owns(owner *cooperative.Owner, scope *cooperative.Scope) bool {
	return b != nil && b.owner == owner && b.scope == scope && scope.BelongsTo(owner) && b.proof.Owns(owner, scope)
}
func (b *NativeBedrock) Client(ctx context.Context, model string) (NativeBedrockClient, error) {
	if !b.Owns(b.owner, b.scope) {
		return NativeBedrockClient{}, cooperative.ErrRestricted
	}
	return b.client(ctx, model)
}
