package builtinwrite

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

type CompactFileState struct {
	Path  string
	State FileState
}

func (c *FileStateCache) CompactEntries(scope *cooperative.Scope) ([]CompactFileState, error) {
	if e := c.check(scope); e != nil {
		return nil, e
	}
	out := []CompactFileState{}
	for cursor := c.order.Front(); cursor != nil; cursor = cursor.Next() {
		v := cursor.Value.(cacheEntry)
		out = append(out, CompactFileState{v.key, copyState(v.value)})
	}
	return out, nil
}
func (c *FileStateCache) ClearAfterCompact(scope *cooperative.Scope) error {
	if e := c.check(scope); e != nil {
		return e
	}
	c.order.Init()
	clear(c.entries)
	c.bytes = 0
	return nil
}
