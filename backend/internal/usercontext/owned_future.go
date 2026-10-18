package usercontext

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AwaitOnOwner waits for the actual shared memory producer's immutable result.
// The model AbortSignal does not cancel that Source memo. No physical worker is
// synthesized when the memo is already fulfilled; pending memory can finish
// while the canonical Owner continues its admitted input/read callbacks.
func (f *Future) AwaitOnOwner(scope *cooperative.Scope) (*protocol.JSONObject, error) {
	if f == nil || scope == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.AwaitPromise(f.done); err != nil {
		return nil, err
	}
	return f.Await(context.Background())
}
