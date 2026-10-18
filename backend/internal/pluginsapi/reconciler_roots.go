package pluginsapi

import (
	"container/list"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// MarketplaceRoots is the native process's two original git memo lifetimes.
// It belongs to plugin bindings, never to a package-global cache shared by
// independent native sessions.
type MarketplaceRoots struct {
	mu                sync.Mutex
	search, canonical marketplaceRootLRU
}

type marketplaceRootValue struct{ key, value string }
type marketplaceRootLRU struct {
	order   list.List
	entries map[string]*list.Element
	maximum int
}

func NewMarketplaceRoots() *MarketplaceRoots {
	return &MarketplaceRoots{search: marketplaceRootLRU{maximum: 512}, canonical: marketplaceRootLRU{maximum: 50}}
}
func (cache *marketplaceRootLRU) get(key string, load func() string) string {
	if cache.entries == nil {
		cache.entries = map[string]*list.Element{}
	}
	if element := cache.entries[key]; element != nil {
		cache.order.MoveToFront(element)
		return element.Value.(marketplaceRootValue).value
	}
	value := load()
	cache.entries[key] = cache.order.PushFront(marketplaceRootValue{key, value})
	if cache.order.Len() > cache.maximum {
		old := cache.order.Back()
		delete(cache.entries, old.Value.(marketplaceRootValue).key)
		cache.order.Remove(old)
	}
	return value
}
func (roots *MarketplaceRoots) Canonical(start string) string {
	roots.mu.Lock()
	defer roots.mu.Unlock()
	root := roots.search.get(start, func() string {
		absolute, err := filepath.Abs(start)
		if err != nil {
			return ""
		}
		return usercontext.GitRoot(usercontext.OSMemoryFiles{}, absolute)
	})
	if root == "" {
		return ""
	}
	return roots.canonical.get(root, func() string {
		return usercontext.ResolveCanonicalGitRoot(usercontext.OSMemoryFiles{}, filepath.EvalSymlinks, root)
	})
}
