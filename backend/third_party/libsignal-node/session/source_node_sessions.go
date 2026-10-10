package session

import (
	"context"

	"go.mau.fi/libsignal/keys/message"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/signalerror"
	"go.mau.fi/libsignal/state/record"
)

type nodeSessionContextKey struct{}
type nodeTransactionContextKey struct{}
type nodeReleaseContextKey struct{}
type NodeSessionAcquire func(context.Context, []string) (context.Context, func(), error)

func WithNodeSessionTransactions(ctx context.Context, acquire NodeSessionAcquire) context.Context {
	return context.WithValue(WithNodeSessionSemantics(ctx), nodeTransactionContextKey{}, acquire)
}

func BeginNodeSessionTransactions(ctx context.Context, addresses []string) (context.Context, func(), error) {
	acquire, _ := ctx.Value(nodeTransactionContextKey{}).(NodeSessionAcquire)
	if !nodeSessionSemantics(ctx) || acquire == nil {
		return ctx, func() {}, nil
	}
	return acquire(ctx, addresses)
}

func WithNodeSessionRelease(ctx context.Context, release func()) context.Context {
	return context.WithValue(ctx, nodeReleaseContextKey{}, release)
}
func ReleaseNodeSessions(ctx context.Context) {
	if release, ok := ctx.Value(nodeReleaseContextKey{}).(func()); ok {
		release()
	}
}

// WithNodeSessionSemantics opts only the old Baileys directory consumer into
// its archive semantics. The default library consumer is unchanged.
func WithNodeSessionSemantics(ctx context.Context) context.Context {
	return context.WithValue(ctx, nodeSessionContextKey{}, true)
}

func nodeSessionSemantics(ctx context.Context) bool {
	active, _ := ctx.Value(nodeSessionContextKey{}).(bool)
	return active
}

func (d *Cipher) decryptNodeRecord(ctx context.Context, r *record.Session, ciphertext *protocol.SignalMessage) ([]byte, *message.Keys, error) {
	candidates := append([]*record.State{r.SessionState()}, r.PreviousSessionStates()...)
	for i, original := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		candidate, err := original.CopyForDecryption()
		if err != nil {
			return nil, nil, err
		}
		plain, keys, err := d.DecryptWithState(ctx, candidate, ciphertext)
		if err != nil {
			continue
		}
		if i == 0 {
			r.SetState(candidate)
		} else {
			r.PreviousSessionStates()[i-1] = candidate
		}
		return plain, keys, nil
	}
	return nil, nil, signalerror.ErrNoValidSessions
}
