package providernative

import (
	"context"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type headInitializationKey struct{}

// HeadInitialization is minted only by the actual Owner-bound HEAD transport.
// It retains no Scope or callbacks and cannot be reconstructed from JSON.
type HeadInitialization struct {
	owner     *cooperative.Owner
	transport *headTransport
	target    string
	native    *provider.NativeHTTP1HeadInitialization
}

func BindHeadInitialization(owner *cooperative.Owner, scope *cooperative.Scope, client *http.Client, request *http.Request) (*http.Request, *HeadInitialization, error) {
	if err := (&Boundary{owner, scope}).CheckOwner(); err != nil {
		return nil, nil, err
	}
	if client == nil || request == nil || request.URL == nil || request.Method != http.MethodHead || request.URL.User != nil {
		return nil, nil, cooperative.ErrRestricted
	}
	transport, ok := client.Transport.(*headTransport)
	if !ok {
		// Other platform transports have no such native reservation producer.
		return request, nil, nil
	}
	if transport.owner != owner || transport.pool == nil {
		return nil, nil, cooperative.ErrRestricted
	}
	initialization := &HeadInitialization{owner, transport, request.URL.String(), provider.NewNativeHTTP1HeadInitialization()}
	request = request.WithContext(context.WithValue(request.Context(), headInitializationKey{}, initialization))
	return request, initialization, nil
}

// Await waits for this native request's initialization, or the original fetch
// rejecting before initialization. It never waits for headers/completion when
// a real request was admitted, and never changes that independent fetch's ctx.
func (initialization *HeadInitialization) Await(scope *cooperative.Scope, fetchFinished <-chan struct{}) (bool, error) {
	if initialization == nil || initialization.owner == nil || initialization.transport == nil || initialization.native == nil || fetchFinished == nil {
		return false, cooperative.ErrRestricted
	}
	if err := (&Boundary{initialization.owner, scope}).CheckOwner(); err != nil {
		return false, err
	}
	value, err := scope.AwaitBytes(cooperative.NetworkOpen, func(ctx context.Context) ([]byte, error) {
		select {
		case <-initialization.native.Done():
			return []byte{1}, nil
		case <-fetchFinished:
			select {
			case <-initialization.native.Done():
				return []byte{1}, nil
			default:
				return nil, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	return len(value) == 1 && value[0] == 1, err
}

func (transport *headTransport) initialization(request *http.Request) *provider.NativeHTTP1HeadInitialization {
	if request == nil {
		return nil
	}
	initialization, _ := request.Context().Value(headInitializationKey{}).(*HeadInitialization)
	if initialization == nil || initialization.transport != transport || initialization.owner != transport.owner || request.Method != http.MethodHead || request.URL == nil || initialization.target != request.URL.String() {
		return nil
	}
	return initialization.native
}
