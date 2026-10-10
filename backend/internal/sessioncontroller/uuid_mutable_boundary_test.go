package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

func TestMutableCacheBoundaryGuards(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	transcript, e := loader.LoadTranscriptFile(writer.target(), false)
	if e != nil {
		t.Fatal(e)
	}
	emptyOwner, emptyWriter, emptyPending, emptyLoader := uuidHarness(t, "", nil, false)
	empty, e := emptyLoader.LoadTranscriptFile(emptyWriter.target(), false)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = pending.PrimeSessionUUIDCache(nil, loader, "sid", transcript); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, _, e = pending.LookupSessionUUIDCache(nil, "sid"); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := emptyOwner.Run(context.Background(), func(s *cooperative.Scope) error {
		if _, _, e := pending.PrimeSessionUUIDCache(s, loader, "sid", transcript); !errors.Is(e, cooperative.ErrRestricted) {
			return ioUnexpected()
		}
		_, _, e := pending.LookupSessionUUIDCache(s, "sid")
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		for _, sid := range []string{"", "x/y", "x\\y", "x\x00y", string([]byte{0xff})} {
			if _, _, e := pending.PrimeSessionUUIDCache(s, loader, sid, transcript); e == nil {
				return ioUnexpected()
			}
		}
		for _, item := range []struct {
			loader     *sessionhistory.Loader
			transcript *sessionhistory.Transcript
		}{{nil, transcript}, {loader, nil}} {
			if _, _, e := pending.PrimeSessionUUIDCache(s, item.loader, "sid", item.transcript); e == nil {
				return ioUnexpected()
			}
		}
		f, inserted, e := pending.PrimeSessionUUIDCache(s, loader, "sid", transcript)
		if e != nil || !inserted {
			return ioUnexpected()
		}
		set, e := f.Wait(context.Background())
		if e != nil {
			return e
		}
		for _, raw := range []json.RawMessage{json.RawMessage(`{"k":1}`), json.RawMessage(`[1]`), json.RawMessage(``), json.RawMessage(` `), json.RawMessage(`bad`), json.RawMessage([]byte{0xff})} {
			if e := set.Add(s, raw); !errors.Is(e, sessionhistory.ErrUnsupportedMessageKey) {
				return ioUnexpected()
			}
			if _, e := set.Delete(s, raw); !errors.Is(e, sessionhistory.ErrUnsupportedMessageKey) {
				return ioUnexpected()
			}
			if _, e := set.OwnerHas(s, raw); !errors.Is(e, sessionhistory.ErrUnsupportedMessageKey) {
				return ioUnexpected()
			}
		}
		keys, e := set.OwnerKeys(s)
		if e != nil || len(keys) != 1 {
			return ioUnexpected()
		}
		raw := keys[0].Raw()
		raw[0] = 'x'
		again, e := set.OwnerKeys(s)
		if e != nil || !bytes.Equal(again[0].Raw(), json.RawMessage(`"original-old"`)) {
			return ioUnexpected()
		}
		input := json.RawMessage(`"added"`)
		if e = set.Add(s, input); e != nil {
			return e
		}
		input[1] = 'X'
		if found, e := set.OwnerHas(s, json.RawMessage(`"added"`)); e != nil || !found {
			return ioUnexpected()
		}
		same, inserted, e := pending.PrimeSessionUUIDCache(s, loader, "sid", transcript)
		if e != nil || inserted || same != f {
			return ioUnexpected()
		}
		if f, inserted, e := pending.PrimeSessionUUIDCache(s, loader, "sid", empty); e != nil || inserted || f != nil {
			return ioUnexpected()
		}
		if _, _, e := pending.PrimeSessionUUIDCache(s, emptyLoader, "sid", empty); e == nil {
			return ioUnexpected()
		}
		return nil
	})
	pendingCall(t, emptyOwner, cooperative.Read, func(s *cooperative.Scope) error {
		if f, inserted, e := emptyPending.PrimeSessionUUIDCache(s, emptyLoader, "sid", empty); e != nil || inserted || f != nil {
			return ioUnexpected()
		}
		_, found, e := emptyPending.LookupSessionUUIDCache(s, "sid")
		if found || e != nil {
			return ioUnexpected()
		}
		if emptyPending.uuidCache != nil {
			return ioUnexpected()
		}
		return nil
	})
	// Readonly loader observations do not acquire a mutable host owner by default.
	unbound := transcript.MessageSet()
	if e := unbound.Add(nil, json.RawMessage(`"x"`)); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	owned := transcript.OwnedMessageSet(owner)
	if e := owned.Add(nil, json.RawMessage(`"x"`)); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := owned.Delete(nil, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := owned.OwnerHas(nil, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := owned.OwnerKeys(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := owned.Clear(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := emptyOwner.Run(context.Background(), func(s *cooperative.Scope) error { return owned.Add(s, nil) }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := pending.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		if _, _, e := pending.PrimeSessionUUIDCache(s, loader, "sid", transcript); !errors.Is(e, ErrPendingClosed) {
			return ioUnexpected()
		}
		_, _, e := pending.LookupSessionUUIDCache(s, "sid")
		return expectPendingClosed(e)
	})
}
func expectPendingClosed(e error) error {
	if !errors.Is(e, ErrPendingClosed) {
		return ioUnexpected()
	}
	return nil
}
func TestMutableSetOwnerLifecycleDoesNotCloneOrInvalidate(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	f := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	set, e := f.Wait(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var saved *cooperative.Scope
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		saved = s
		if e := set.Add(s, nil); e != nil {
			return e
		}
		return nil
	})
	if e := set.Add(saved, json.RawMessage(`"outside-owner-task"`)); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("released scope admitted mutation", e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		if e := pending.ClearSessionUUIDCache(s); e != nil {
			return e
		}
		if e := set.Clear(s); e != nil {
			return e
		}
		if e := set.Add(s, json.RawMessage(`"old-still-live"`)); e != nil {
			return e
		}
		return nil
	})
	newer := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	newSet, e := newer.Wait(context.Background())
	if e != nil || newSet == set || newer == f {
		t.Fatal("clear retargeted old promise/set", e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		old, e := set.OwnerHas(s, json.RawMessage(`"old-still-live"`))
		if e != nil || !old {
			return ioUnexpected()
		}
		newer, e := newSet.OwnerHas(s, json.RawMessage(`"old-still-live"`))
		if e != nil || newer {
			return ioUnexpected()
		}
		return nil
	})
	if found, _ := writer.Has(json.RawMessage(`"old-still-live"`)); found {
		t.Fatal("new cache dual-wrote Writer.members")
	}
}
