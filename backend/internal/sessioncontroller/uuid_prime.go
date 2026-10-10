package sessioncontroller

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"strings"
	"unicode/utf8"
)

// PrimeSessionUUIDCache is the real getLastSessionLog cache admission boundary:
// after an independently loaded nonempty Transcript, preserve any existing
// Promise (including an unsettled one), otherwise cache exactly one owned Set.
// The caller must supply the actual Go Loader result, not selected/serialized
// branch UUIDs. It does not mutate Writer.members or select a resume log.
func (pending *PendingWriter) PrimeSessionUUIDCache(scope *cooperative.Scope, loader *sessionhistory.Loader, sid string, transcript *sessionhistory.Transcript) (future *SessionUUIDFuture, inserted bool, err error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, false, cooperative.ErrRestricted
	}
	if loader == nil || transcript == nil {
		return nil, false, errors.New("UUID cache prime requires the real independently loaded transcript")
	}
	if sid == "" || !utf8.ValidString(sid) || strings.ContainsAny(sid, "/\\\x00") {
		return nil, false, capability("original session SID UTF8 leaf")
	}
	err = scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		if pending.uuidCache != nil && pending.uuidCache.loader != loader {
			return errors.New("session UUID cache already belongs to another loader")
		}
		// Source returns before inspecting/creating the cache when messages is empty.
		if transcript.MessageCount() == 0 {
			return nil
		}
		if pending.uuidCache == nil {
			pending.uuidCache = &sessionUUIDCache{loader: loader, promises: map[string]*SessionUUIDFuture{}}
		}
		if existing := pending.uuidCache.promises[sid]; existing != nil {
			future = existing
			return nil
		}
		future = &SessionUUIDFuture{future: newWriteFuture(), owner: pending.owner, set: transcript.OwnedMessageSet(pending.owner)}
		pending.uuidCache.promises[sid] = future
		inserted = true
		future.future.settle(nil)
		return nil
	})
	return
}

// LookupSessionUUIDCache only observes the actual owner-held promise entry.
// Clear removes this entry, never the Set held by a previous consumer.
func (pending *PendingWriter) LookupSessionUUIDCache(scope *cooperative.Scope, sid string) (future *SessionUUIDFuture, exists bool, err error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, false, cooperative.ErrRestricted
	}
	err = scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		if pending.uuidCache != nil {
			future, exists = pending.uuidCache.promises[sid]
		}
		return nil
	})
	return
}
