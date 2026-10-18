package nativemodule

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// NewOwnerInitialized declares an available compiled module whose actual export
// bindings and module state have not been initialized. It does not fetch files
// or imitate Bun's physical producer duration. NewPrelinked remains the entry
// for an already linked/evaluated namespace, with its original warm graph.
//
// namespace is a stable declaration identity; validate checks admission identity
// while link/evaluate install the real exports/state exactly once on Owner. The
// namespace's own export methods must continue to reject use before evaluation.
func NewOwnerInitialized[K comparable, N comparable](owner *cooperative.Owner, scope *cooperative.Scope, key K, namespace N, validate func(*cooperative.Scope, K, N) error, link, evaluate func(*cooperative.Scope) error) (*Entry[K, N], error) {
	var absent N
	if owner == nil || !scope.BelongsTo(owner) || namespace == absent || validate == nil || link == nil || evaluate == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	if err := validate(scope, key, namespace); err != nil {
		return nil, err
	}
	entry := &Entry[K, N]{owner: owner, key: key, namespace: namespace, validate: validate}
	var err error
	if entry.fetch, err = ownerasync.NewOwnerFuture[struct{}](owner); err != nil {
		return nil, err
	}
	if entry.load, err = ownerasync.NewOwnerFuture[*Entry[K, N]](owner); err != nil {
		_ = entry.fetch.RejectAdministrative(err)
		return nil, err
	}
	if entry.evaluation, err = ownerasync.NewOwnerFuture[struct{}](owner); err != nil {
		_ = entry.fetch.RejectAdministrative(err)
		_ = entry.load.RejectAdministrative(err)
		return nil, err
	}
	abandon := func(failure error) {
		_ = entry.fetch.RejectAdministrative(failure)
		_ = entry.load.RejectAdministrative(failure)
		_ = entry.evaluation.RejectAdministrative(failure)
	}
	// The Go code is already compiled: this is not a pending file-fetch claim.
	if err = entry.fetch.Resolve(scope, struct{}{}); err != nil {
		abandon(err)
		return nil, err
	}
	_, err = scope.SubmitModuleInitialization(owner, func(next *cooperative.Scope) error {
		fail := func(failure error) error {
			entry.failure = failure
			// Returning a Receipt error alone would leave the import promises
			// pending. Preserve the real construction error on both obligations.
			_ = entry.load.Reject(next, failure)
			_ = entry.evaluation.Reject(next, failure)
			return failure
		}
		if err := entry.check(next); err != nil {
			return fail(err)
		}
		if err := link(next); err != nil {
			return fail(err)
		}
		if err := entry.check(next); err != nil {
			return fail(err)
		}
		if err := entry.load.Resolve(next, entry); err != nil {
			return fail(err)
		}
		if err := evaluate(next); err != nil {
			return fail(err)
		}
		if err := entry.check(next); err != nil {
			return fail(err)
		}
		if err := entry.evaluation.Resolve(next, struct{}{}); err != nil {
			return fail(err)
		}
		return nil
	})
	if err != nil {
		abandon(err)
		return nil, err
	}
	return entry, nil
}
