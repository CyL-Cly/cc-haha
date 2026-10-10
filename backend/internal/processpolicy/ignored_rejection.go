// Package processpolicy holds one process/Owner rejection authority. Admission
// of a native unhandled event is separate: no Promise turn is invented here.
package processpolicy

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"os"
)

type Kind string

const (
	Native       Kind = "native"
	CLIHelper    Kind = "cli-helper"
	ServerHelper Kind = "server-helper"
)

type Fault struct {
	Original      error
	Name, Message string
}
type Event struct {
	Kind, Severity  string
	Fault           Fault
	Promise, Holder *ownerasync.PromiseIdentity
	Site            string
}
type Options struct {
	Owner       *cooperative.Owner
	Kind        Kind
	WriteStderr func([]byte) error
	Diagnostic  func(Event) error
}

// Authority is never global-by-default. A production host must explicitly lend
// the same instance to Record and permission consumers on the same process.
type Authority struct {
	options Options
	pid     int
	fatal   bool
	events  []Event
	seen    map[*ownerasync.PromiseIdentity]bool
}

func New(options Options) (*Authority, error) {
	if options.Owner == nil || options.WriteStderr == nil {
		return nil, cooperative.ErrRestricted
	}
	switch options.Kind {
	case Native, CLIHelper, ServerHelper:
	default:
		return nil, cooperative.ErrRestricted
	}
	return &Authority{options: options, pid: os.Getpid(), seen: map[*ownerasync.PromiseIdentity]bool{}}, nil
}
func (a *Authority) Owns(owner *cooperative.Owner, kind Kind) bool {
	return a != nil && a.pid == os.Getpid() && a.options.Owner == owner && a.options.Kind == kind
}
func (a *Authority) check(scope *cooperative.Scope) error {
	if a == nil || a.pid != os.Getpid() || !scope.BelongsTo(a.options.Owner) {
		return cooperative.ErrRestricted
	}
	return nil
}

// Dispatch handles only an event already admitted by the actual host runtime.
// Pending(scope) is NOT that admission. No handler is attached to its Promise.
func (a *Authority) Dispatch(scope *cooperative.Scope, notice ownerasync.IgnoredRejection) error {
	if err := a.check(scope); err != nil {
		return err
	}
	if notice.Promise == nil || notice.Holder == nil || notice.Promise == notice.Holder || notice.Original == nil || notice.Site == "" || errors.Is(notice.Original, cooperative.ErrClosed) {
		return cooperative.ErrRestricted
	}
	if a.seen[notice.Promise] {
		return nil
	}
	a.seen[notice.Promise] = true
	name := "Error"
	if named, ok := notice.Original.(interface{ Name() string }); ok {
		name = named.Name()
	}
	return a.handle(scope, Fault{Original: notice.Original, Name: name, Message: notice.Original.Error()}, notice.Promise, notice.Holder, notice.Site)
}

// RecordFault lets the existing exact native Record adapter share this ledger
// without changing its EACCES projection or conflating it with permission Error.
func (a *Authority) RecordFault(scope *cooperative.Scope, fault Fault) error {
	return a.handle(scope, fault, nil, nil, "record")
}
func (a *Authority) handle(scope *cooperative.Scope, fault Fault, promise, holder *ownerasync.PromiseIdentity, site string) error {
	if err := a.check(scope); err != nil {
		return err
	}
	if fault.Original == nil || fault.Name == "" {
		return cooperative.ErrRestricted
	}
	switch a.options.Kind {
	case Native:
		a.fatal = true
		return a.options.WriteStderr([]byte(fault.Name + ": " + fault.Message + "\n"))
	case CLIHelper:
		return a.diagnostic(Event{Kind: "unhandled_rejection", Severity: "error", Fault: fault, Promise: promise, Holder: holder, Site: site})
	case ServerHelper:
		if err := a.options.WriteStderr([]byte("[Server] Unhandled rejection:\n" + fault.Name + ": " + fault.Message + "\n")); err != nil {
			return err
		}
		return a.diagnostic(Event{Kind: "server_unhandled_rejection", Severity: "error", Fault: fault, Promise: promise, Holder: holder, Site: site})
	}
	return cooperative.ErrRestricted
}
func (a *Authority) diagnostic(event Event) error {
	a.events = append(a.events, event)
	if a.options.Diagnostic != nil {
		return a.options.Diagnostic(event)
	}
	return nil
}
func (a *Authority) Events(scope *cooperative.Scope) ([]Event, error) {
	if err := a.check(scope); err != nil {
		return nil, err
	}
	return append([]Event{}, a.events...), nil
}
func (a *Authority) ExitCode(scope *cooperative.Scope, ordinary int) (int, error) {
	if err := a.check(scope); err != nil {
		return 0, err
	}
	if a.fatal {
		return 1, nil
	}
	return ordinary, nil
}
