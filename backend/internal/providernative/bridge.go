// Package providernative binds the one canonical owner to the finite Provider
// boundary. It constructs no loop, default callbacks, identity await or policy.
package providernative

import (
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type Boundary struct {
	owner *cooperative.Owner
	scope *cooperative.Scope
}

func New(owner *cooperative.Owner, scope *cooperative.Scope, observe func(provider.OwnerNativeEvent)) (*provider.OwnerNativeIO, error) {
	return provider.NewOwnerNativeIO(&Boundary{owner, scope}, observe)
}

// Factory captures the original owner and scope, never an admitted or prepared
// request. A factory retained after its ASK cannot admit the next ASK.
type Factory struct {
	boundary *Boundary
	observe  func(provider.OwnerNativeEvent)
}

func NewFactory(owner *cooperative.Owner, scope *cooperative.Scope, observe func(provider.OwnerNativeEvent)) *Factory {
	return &Factory{boundary: &Boundary{owner, scope}, observe: observe}
}
func (factory *Factory) CheckOwner() error {
	if factory == nil {
		return provider.CheckNativeOwnerBoundary(nil)
	}
	return provider.CheckNativeOwnerBoundary(factory.boundary)
}
func (factory *Factory) NewNativeOpenIO() (*provider.OwnerNativeIO, error) {
	if factory == nil {
		return provider.NewOwnerNativeIO(nil, nil)
	}
	return provider.NewOwnerNativeIO(factory.boundary, factory.observe)
}

func (boundary *Boundary) CheckOwner() error {
	if boundary == nil || boundary.owner == nil || !boundary.scope.BelongsTo(boundary.owner) {
		return cooperative.ErrRestricted
	}
	return nil
}
func (boundary *Boundary) ArmFetchTimer(delay time.Duration, callback func()) (provider.NativeOwnerTimer, error) {
	if err := boundary.CheckOwner(); err != nil {
		return nil, err
	}
	ticket, err := boundary.scope.ArmTimer(delay, callback)
	if err != nil {
		return nil, err
	}
	return &timer{boundary.scope, ticket}, nil
}
func (boundary *Boundary) AwaitHeaders(operation provider.NativeOwnerOperation) ([]byte, error) {
	if err := boundary.CheckOwner(); err != nil {
		return nil, err
	}
	return boundary.scope.AwaitNative(operation)
}
func (boundary *Boundary) RejectFetch(failure error) error {
	if err := boundary.CheckOwner(); err != nil {
		return err
	}
	return boundary.scope.RejectFetch(failure)
}

func (boundary *Boundary) IsAdministrative(err error) bool {
	return errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop)
}

type timer struct {
	scope  *cooperative.Scope
	ticket *cooperative.TimerTicket
}

func (ticket *timer) Clear() error { return ticket.scope.ClearTimer(ticket.ticket) }

var _ provider.NativeOwnerBoundary = (*Boundary)(nil)
