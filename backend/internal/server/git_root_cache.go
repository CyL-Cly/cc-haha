package server

import (
	"container/list"
	"golang.org/x/text/unicode/norm"
)

type gitRootValue struct{ key, value string }
type gitRootCache struct {
	order   *list.List
	entries map[string]*list.Element
	maximum int
}

func newGitRootCache(maximum int) *gitRootCache {
	return &gitRootCache{order: list.New(), entries: map[string]*list.Element{}, maximum: maximum}
}
func (cache *gitRootCache) get(key string, load func() string) string {
	if element := cache.entries[key]; element != nil {
		cache.order.MoveToFront(element)
		return element.Value.(gitRootValue).value
	}
	value := load()
	cache.entries[key] = cache.order.PushFront(gitRootValue{key, value})
	if cache.order.Len() > cache.maximum {
		old := cache.order.Back()
		delete(cache.entries, old.Value.(gitRootValue).key)
		cache.order.Remove(old)
	}
	return value
}

// Source caches both positive and negative lookup outcomes until LRU eviction
// or process restart. Key identity is the original start path, before resolve.
func (server *Server) findCachedGitRoot(start string) string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.gitRoots == nil {
		server.gitRoots = newGitRootCache(512)
	}
	return server.gitRoots.get(start, func() string { return norm.NFC.String(nearestSearchGitRoot(server.filesystemPath(start))) })
}
func (server *Server) findCachedCanonicalGitRoot(start string) string {
	root := server.findCachedGitRoot(start)
	if root == "" {
		return ""
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.gitCanonicalRoots == nil {
		server.gitCanonicalRoots = newGitRootCache(50)
	}
	return server.gitCanonicalRoots.get(root, func() string { return resolveCanonicalGitRoot(root) })
}
