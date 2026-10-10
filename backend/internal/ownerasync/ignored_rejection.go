package ownerasync

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"sync"
)

// PromiseIdentity is opaque and allocated for each actual Promise, independent
// of business values. It is not a rendered ID supplied by an observer.
type PromiseIdentity struct{ marker byte }

func (future *OwnerFuture[T]) Identity() *PromiseIdentity {
	if future == nil {
		return nil
	}
	return future.identity
}

// IgnoredRejection is an immutable runtime observation, not a business handler.
// Original is preserved by identity; administrative closure is never a notice.
type IgnoredRejection struct {
	Promise, Holder *PromiseIdentity
	Site            string
	Original        error
}
type ignoredEntry struct {
	notification IgnoredRejection
	handled      bool
}

// IgnoredRejections records actual dependent settlements without attaching a
// Then/catch or pumping Owner. Pending does NOT choose Bun's unhandled turn or
// dispatch policy. That physical-runtime checkpoint remains unimplemented.
type IgnoredRejections struct {
	owner   *cooperative.Owner
	mu      sync.Mutex
	entries []ignoredEntry
}

func NewIgnoredRejections(owner *cooperative.Owner) (*IgnoredRejections, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &IgnoredRejections{owner: owner}, nil
}
func (tracker *IgnoredRejections) Owns(owner *cooperative.Owner) bool {
	return tracker != nil && owner != nil && tracker.owner == owner
}
func Ignore[T any](scope *cooperative.Scope, future *OwnerFuture[T], tracker *IgnoredRejections, holder *PromiseIdentity, site string) error {
	if tracker == nil || future == nil || holder == nil || holder == future.Identity() || site == "" || !scope.BelongsTo(tracker.owner) || future.owner != tracker.owner {
		return cooperative.ErrRestricted
	}
	if err := future.check(scope); err != nil {
		return err
	}
	future.mu.Lock()
	defer future.mu.Unlock()
	if future.ignored != nil {
		if future.ignored != tracker || future.ignoredHolder != holder || future.ignoredSite != site {
			return cooperative.ErrRestricted
		}
		return nil
	}
	future.ignored, future.ignoredHolder, future.ignoredSite = tracker, holder, site
	if future.settled && future.err != nil && !future.administrative {
		tracker.rejected(future.identity, holder, site, future.err, future.handled)
	}
	return nil
}
func (tracker *IgnoredRejections) rejected(id, holder *PromiseIdentity, site string, reason error, handled bool) {
	if reason == nil || errors.Is(reason, cooperative.ErrClosed) {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for _, entry := range tracker.entries {
		if entry.notification.Promise == id {
			return
		}
	}
	tracker.entries = append(tracker.entries, ignoredEntry{notification: IgnoredRejection{Promise: id, Holder: holder, Site: site, Original: reason}, handled: handled})
}
func (tracker *IgnoredRejections) handled(id *PromiseIdentity) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for i := range tracker.entries {
		if tracker.entries[i].notification.Promise == id {
			tracker.entries[i].handled = true
		}
	}
}
func (tracker *IgnoredRejections) Pending(scope *cooperative.Scope) ([]IgnoredRejection, error) {
	if tracker == nil || !scope.BelongsTo(tracker.owner) {
		return nil, cooperative.ErrRestricted
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	out := []IgnoredRejection{}
	for _, entry := range tracker.entries {
		if !entry.handled {
			out = append(out, entry.notification)
		}
	}
	return out, nil
}

// RuntimeCheckpoint explicitly refuses to guess native turn ordering. Callers
// can inspect Pending, but must not turn that inspection into a model await or
// an invented native event/exit boundary.
func (tracker *IgnoredRejections) RuntimeCheckpoint(scope *cooperative.Scope) error {
	if tracker == nil || !scope.BelongsTo(tracker.owner) {
		return cooperative.ErrRestricted
	}
	return &UnhandledTurnCapabilityError{}
}

type UnhandledTurnCapabilityError struct{}

func (*UnhandledTurnCapabilityError) Error() string {
	return "native unhandled Promise turn checkpoint is not migrated"
}
