package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type NativeVertexClient struct {
	BaseURL string
	Config  *provider.VertexConfig
}

// NativeVertex is minted by the actual process's shared cloud producer for
// the current ASK. It carries no prepared request or caller-supplied credential.
type NativeVertex struct {
	owner     *cooperative.Owner
	scope     *cooperative.Scope
	client    func(context.Context, string) (NativeVertexClient, error)
	canonical func(string) (string, error)
	proof     *cooperative.NativeVertexRequest
}

func NewNativeVertex(owner *cooperative.Owner, scope *cooperative.Scope, client func(context.Context, string) (NativeVertexClient, error), canonical ...func(string) (string, error)) (*NativeVertex, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) || client == nil {
		return nil, cooperative.ErrRestricted
	}
	proof, err := cooperative.NewNativeVertexRequest(owner, scope)
	if err != nil {
		return nil, err
	}
	value := &NativeVertex{owner: owner, scope: scope, client: client, proof: proof}
	if len(canonical) > 1 {
		return nil, cooperative.ErrRestricted
	}
	if len(canonical) == 1 {
		value.canonical = canonical[0]
	}
	return value, nil
}
func (b *NativeVertex) Canonical(model string) (string, error) {
	if !b.Owns(b.owner, b.scope) || b.canonical == nil {
		return "", cooperative.ErrRestricted
	}
	return b.canonical(model)
}

func BindNativeVertexHost(input *Input, deps Dependencies) error {
	if deps.NativeCloudError != nil {
		return deps.NativeCloudError
	}
	if deps.NativeVertex == nil {
		return nil
	}
	if input == nil || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || !deps.NativeVertex.Owns(deps.StreamOwner, deps.StreamScope) {
		return cooperative.ErrRestricted
	}
	if openAIEnvTruthy(input.Context.Host.Env["CLAUDE_CODE_USE_VERTEX"]) {
		input.Context.Host.NativeVertexBound = true
		input.Context.Host.NativeVertexProof = deps.NativeVertex.proof
		input.Context.Host.NativeVertexCanonical = deps.NativeVertex.Canonical
	}
	return nil
}
func (b *NativeVertex) Owns(owner *cooperative.Owner, scope *cooperative.Scope) bool {
	return b != nil && b.owner == owner && b.scope == scope && scope.BelongsTo(owner) && b.proof.Owns(owner, scope)
}
func (b *NativeVertex) Client(ctx context.Context, model string) (NativeVertexClient, error) {
	if !b.Owns(b.owner, b.scope) {
		return NativeVertexClient{}, cooperative.ErrRestricted
	}
	return b.client(ctx, model)
}
