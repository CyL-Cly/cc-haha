package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// The transcript controller is the sole restored-history producer. Tools must
// consume its actual Read/Write/Edit state instead of starting a second empty
// native cache after --resume, fork or a newly constructed rewind runtime.
func (m *MainPrint) restoreNativeFileCache(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) || m.nativeFileCache == nil {
		return cooperative.ErrRestricted
	}
	if d := m.options.DetachedAgent; d != nil && d.inheritedFileCache != nil {
		return m.nativeFileCache.Import(scope, d.inheritedFileCache)
	}
	if err := m.nativeFileCache.Clear(scope); err != nil {
		return err
	}
	cache := m.Controller.ReadCache()
	entries := cache.Entries()
	// Source caches retain recency. Insert oldest first so Set's MRU order and
	// byte/entry eviction agree, while Get keeps the original NaN timestamp.
	for i := len(entries) - 1; i >= 0; i-- {
		path := entries[i].String("path")
		state, exists := cache.Get(path)
		if !exists {
			continue
		}
		if err := m.nativeFileCache.Set(scope, path, builtinwrite.FileState{Content: state.Content, Timestamp: state.Timestamp}); err != nil {
			return err
		}
	}
	return nil
}
