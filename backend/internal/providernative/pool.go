package providernative

import (
	"net/http"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// Pool binds a process HTTP resource to one Owner. Each independent child gets
// its own binding; no request, Scope or credential crosses between Owners.
type Pool struct {
	owner    *cooperative.Owner
	io       *provider.NativeHTTP1Pool
	resource *poolResource
	borrowed bool
	closed   atomic.Bool
}

type poolResource struct {
	io     *provider.NativeHTTP1Pool
	closed atomic.Bool
}

// PoolTransfer is minted by the parent's active Owner. The actual Agent
// constructor can bind this physical resource on its new Owner without an
// extra async operation. It retains no admitted Scope or prepared request.
type PoolTransfer struct {
	source   *Pool
	resource *poolResource
}

func NewPool(owner *cooperative.Owner, scope *cooperative.Scope) (*Pool, error) {
	if err := (&Boundary{owner, scope}).CheckOwner(); err != nil {
		return nil, err
	}
	resource := &poolResource{io: provider.NewNativeHTTP1Pool()}
	return &Pool{owner: owner, io: resource.io, resource: resource}, nil
}
func (pool *Pool) Owns(owner *cooperative.Owner) bool {
	return pool != nil && owner != nil && pool.owner == owner
}

func (pool *Pool) checkBorrowed() error {
	if pool.borrowed && (pool.closed.Load() || pool.resource.closed.Load()) {
		return cooperative.ErrClosed
	}
	return nil
}

func (pool *Pool) TransferOnOwner(owner *cooperative.Owner, scope *cooperative.Scope) (*PoolTransfer, error) {
	if !pool.Owns(owner) || pool.resource == nil || pool.io == nil || pool.io != pool.resource.io {
		return nil, cooperative.ErrRestricted
	}
	if err := (&Boundary{owner, scope}).CheckOwner(); err != nil {
		return nil, err
	}
	if pool.closed.Load() || pool.resource.closed.Load() {
		return nil, cooperative.ErrClosed
	}
	return &PoolTransfer{source: pool, resource: pool.resource}, nil
}

func (transfer *PoolTransfer) SourceOwns(owner *cooperative.Owner) bool {
	return transfer != nil && transfer.source != nil && transfer.resource != nil &&
		transfer.resource.io != nil &&
		transfer.source.Owns(owner) && transfer.source.resource == transfer.resource &&
		transfer.source.io == transfer.resource.io
}

func (transfer *PoolTransfer) BindOnOwner(owner *cooperative.Owner, scope *cooperative.Scope) (*Pool, error) {
	if transfer == nil || transfer.source == nil || !transfer.SourceOwns(transfer.source.owner) || owner == transfer.source.owner {
		return nil, cooperative.ErrRestricted
	}
	if err := (&Boundary{owner, scope}).CheckOwner(); err != nil {
		return nil, err
	}
	if transfer.source.closed.Load() || transfer.resource.closed.Load() {
		return nil, cooperative.ErrClosed
	}
	return &Pool{owner: owner, io: transfer.resource.io, resource: transfer.resource, borrowed: true}, nil
}

func (pool *Pool) Driver(owner *cooperative.Owner, scope *cooperative.Scope, observe func(provider.OwnerNativeEvent)) (*provider.OwnerNativeIO, error) {
	if !pool.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	if err := pool.checkBorrowed(); err != nil {
		return nil, err
	}
	return provider.NewPooledOwnerNativeIO(&Boundary{owner, scope}, pool.io, observe)
}
func (pool *Pool) Close() error {
	if pool == nil {
		return nil
	}
	if pool.resource == nil || pool.io == nil {
		return cooperative.ErrRestricted
	}
	if pool.closed.Swap(true) || pool.borrowed {
		return nil
	}
	pool.resource.closed.Store(true)
	return pool.io.Close()
}

type headTransport struct {
	owner    *cooperative.Owner
	pool     *provider.NativeHTTP1Pool
	exchange *provider.RawHTTP1Exchange
	binding  *Pool
}

func (transport *headTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := transport.binding.checkBorrowed(); err != nil {
		return nil, err
	}
	return transport.pool.HeadInitialized(request, transport.exchange, transport.initialization(request))
}

// HeadTransport issues only the init HEAD primitive. The transport worker has
// copied TLS/proxy inputs and the physical pool; it never receives this Scope.
func (pool *Pool) HeadTransport(owner *cooperative.Owner, scope *cooperative.Scope, exchange *provider.RawHTTP1Exchange) (http.RoundTripper, error) {
	if !pool.Owns(owner) || exchange == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := (&Boundary{owner, scope}).CheckOwner(); err != nil {
		return nil, err
	}
	if err := pool.checkBorrowed(); err != nil {
		return nil, err
	}
	copy := *exchange
	return &headTransport{owner: pool.owner, pool: pool.io, exchange: &copy, binding: pool}, nil
}
