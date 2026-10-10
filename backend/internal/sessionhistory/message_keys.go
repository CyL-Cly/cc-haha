package sessionhistory

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"unicode/utf8"
)

// MessageKey captures the Map key at insertion, independently of later changes
// to the stored envelope. Object keys retain loader-local identity. Raw JSON
// observers create fresh object identities and cannot match those keys.
type MessageKey struct {
	identity string
	raw      json.RawMessage
}

func (entry MessageKey) Raw() json.RawMessage { return append(json.RawMessage(nil), entry.raw...) }

type MessageSet struct {
	keys    []MessageKey
	members map[string]bool
	owner   *cooperative.Owner
}

func (value *Transcript) MessageSet() *MessageSet {
	set := &MessageSet{members: map[string]bool{}}
	for _, name := range value.order {
		if _, exists := value.messages[name]; !exists {
			continue
		}
		entry := value.messageKeys[name]
		set.keys = append(set.keys, entry)
		set.members[entry.identity] = true
	}
	return set
}

func (set *MessageSet) Has(raw json.RawMessage) bool { return set.members[key(raw, -1)] }
func (set *MessageSet) Len() int                     { return len(set.keys) }
func (set *MessageSet) Keys() []MessageKey {
	values := append([]MessageKey(nil), set.keys...)
	for i := range values {
		values[i].raw = append(json.RawMessage(nil), values[i].raw...)
	}
	return values
}

// ErrUnsupportedMessageKey marks mutation inputs without a migrated neutral
// reference identity. The readonly loader still captures its object Map keys.
type MessageKeyCapabilityError struct{ Boundary string }

func (e *MessageKeyCapabilityError) Error() string {
	return "message Set capability is not migrated: " + e.Boundary
}

var ErrUnsupportedMessageKey = &MessageKeyCapabilityError{Boundary: "original JSON primitive or undefined; object reference identity"}

func primitiveMessageKey(raw json.RawMessage) (MessageKey, error) {
	if raw == nil {
		return MessageKey{identity: key(nil, -1)}, nil
	}
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || !utf8.Valid(value) || !json.Valid(value) || value[0] == '{' || value[0] == '[' {
		return MessageKey{}, ErrUnsupportedMessageKey
	}
	return MessageKey{identity: key(value, -1), raw: append(json.RawMessage(nil), value...)}, nil
}

// OwnedMessageSet is used only when the real cache/prime admits this newly
// constructed Set. It preserves actual loader insertion keys without binding
// the common readonly Transcript.MessageSet observer to a runtime owner.
func (value *Transcript) OwnedMessageSet(owner *cooperative.Owner) *MessageSet {
	set := value.MessageSet()
	set.owner = owner
	return set
}
func (set *MessageSet) onOwner(scope *cooperative.Scope, work func() error) error {
	if set.owner == nil || !scope.BelongsTo(set.owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return work() })
}
func (set *MessageSet) OwnerHas(scope *cooperative.Scope, raw json.RawMessage) (found bool, err error) {
	err = set.onOwner(scope, func() error {
		entry, e := primitiveMessageKey(raw)
		if e != nil {
			return e
		}
		found = set.members[entry.identity]
		return nil
	})
	return
}
func (set *MessageSet) OwnerKeys(scope *cooperative.Scope) (values []MessageKey, err error) {
	err = set.onOwner(scope, func() error { values = set.Keys(); return nil })
	return
}
func (set *MessageSet) Add(scope *cooperative.Scope, raw json.RawMessage) error {
	return set.onOwner(scope, func() error {
		entry, err := primitiveMessageKey(raw)
		if err != nil {
			return err
		}
		if !set.members[entry.identity] {
			set.keys = append(set.keys, entry)
			set.members[entry.identity] = true
		}
		return nil
	})
}
func (set *MessageSet) Delete(scope *cooperative.Scope, raw json.RawMessage) (deleted bool, err error) {
	err = set.onOwner(scope, func() error {
		entry, e := primitiveMessageKey(raw)
		if e != nil {
			return e
		}
		if !set.members[entry.identity] {
			return nil
		}
		delete(set.members, entry.identity)
		for i, k := range set.keys {
			if k.identity == entry.identity {
				set.keys = append(set.keys[:i:i], set.keys[i+1:]...)
				break
			}
		}
		deleted = true
		return nil
	})
	return
}
func (set *MessageSet) Clear(scope *cooperative.Scope) error {
	return set.onOwner(scope, func() error { set.keys = nil; clear(set.members); return nil })
}

// MessageCount observes the actual restored Map size without constructing a Set.
func (value *Transcript) MessageCount() int { return len(value.messages) }
