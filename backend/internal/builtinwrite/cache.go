package builtinwrite

import (
	"container/list"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"unicode/utf16"
)

// FileStateCache is the shared live Read/Write cache. It belongs to the caller's
// one Owner, with Source's normalized keys, entry/UTF8 byte limits and LRU get.
type FileState struct {
	Content       json.RawMessage
	Timestamp     float64
	Offset, Limit *float64
	PartialView   bool
}
type cacheEntry struct {
	key   string
	value FileState
	size  int
}
type FileStateCache struct {
	owner                *cooperative.Owner
	max, maxBytes, bytes int
	order                *list.List
	entries              map[string]*list.Element
}

func NewFileStateCache(owner *cooperative.Owner, max, maxBytes int) (*FileStateCache, error) {
	if owner == nil || max <= 0 || maxBytes <= 0 {
		return nil, cooperative.ErrRestricted
	}
	return &FileStateCache{owner: owner, max: max, maxBytes: maxBytes, order: list.New(), entries: map[string]*list.Element{}}, nil
}
func (c *FileStateCache) Owns(owner *cooperative.Owner) bool {
	return c != nil && owner != nil && c.owner == owner
}
func (c *FileStateCache) check(scope *cooperative.Scope) error {
	if c == nil || !scope.BelongsTo(c.owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
}
func copyState(v FileState) FileState {
	v.Content = append(json.RawMessage(nil), v.Content...)
	if v.Offset != nil {
		n := *v.Offset
		v.Offset = &n
	}
	if v.Limit != nil {
		n := *v.Limit
		v.Limit = &n
	}
	return v
}
func (c *FileStateCache) Get(scope *cooperative.Scope, key string) (FileState, bool, error) {
	if err := c.check(scope); err != nil {
		return FileState{}, false, err
	}
	e := c.entries[posixNormalize(key)]
	if e == nil {
		return FileState{}, false, nil
	}
	c.order.MoveToFront(e)
	return copyState(e.Value.(cacheEntry).value), true, nil
}
func (c *FileStateCache) remove(e *list.Element) {
	v := e.Value.(cacheEntry)
	delete(c.entries, v.key)
	c.bytes -= v.size
	c.order.Remove(e)
}
func (c *FileStateCache) Set(scope *cooperative.Scope, key string, v FileState) error {
	if err := c.check(scope); err != nil {
		return err
	}
	units, err := protocol.DecodeJavaScriptJSONString(v.Content)
	if err != nil {
		return err
	}
	n := len([]byte(string(utf16.Decode(units))))
	if n == 0 {
		n = 1
	}
	key = posixNormalize(key)
	// lru-cache rejects an oversized replacement, removing the previous entry.
	if old := c.entries[key]; old != nil {
		c.remove(old)
	}
	if n > c.maxBytes {
		return nil
	}
	for c.order.Len() >= c.max || c.bytes+n > c.maxBytes {
		c.remove(c.order.Back())
	}
	e := c.order.PushFront(cacheEntry{key, copyState(v), n})
	c.entries[key] = e
	c.bytes += n
	return nil
}
func (c *FileStateCache) Snapshot(scope *cooperative.Scope) (entries, bytes int, err error) {
	if err = c.check(scope); err != nil {
		return
	}
	return c.order.Len(), c.bytes, nil
}
