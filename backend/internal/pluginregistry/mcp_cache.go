package pluginregistry

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

func snapshotResult(result Result) Result {
	result.Enabled = append([]Loaded{}, result.Enabled...)
	result.Disabled = append([]Loaded{}, result.Disabled...)
	result.Errors = append([]protocol.JSONObject{}, result.Errors...)
	return result
}

// PublishMCPCache updates only the unresolved server field of the exact plugin
// load that produced it. A refresh owns new LoadedPlugin objects; completion
// from an older load must not publish into those objects. Returned slices are
// snapshots so native asynchronous readers never borrow mutable struct slots.
func (l *Loader) PublishMCPCache(snapshot Result, objects []protocol.JSONObject) {
	l.publishComponents(snapshot, objects, false)
}

// PublishComponentCache publishes the REPL mount's original lazy component
// slots and error array into the same full/cached promise, with the same load
// identity fence used by MCP discovery.
func (l *Loader) PublishComponentCache(snapshot Result, objects []protocol.JSONObject) {
	l.publishComponents(snapshot, objects, true)
}

func (l *Loader) publishComponents(snapshot Result, objects []protocol.JSONObject, components bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cached == nil || snapshot.cacheID == 0 || snapshot.cacheID != l.cached.cacheID {
		return
	}
	update := func(result *Result) {
		if result == nil || result.cacheID != snapshot.cacheID {
			return
		}
		if components {
			result.Errors = append([]protocol.JSONObject{}, snapshot.Errors...)
		}
		for i, plugin := range result.Enabled {
			for _, value := range objects {
				if value.String("source") != plugin.Source || value.String("path") != plugin.Path {
					continue
				}
				if raw := value.Get("mcpServers"); raw != nil {
					var servers protocol.JSONObject
					if servers.UnmarshalJSON(raw) == nil && servers.IsObject() {
						result.Enabled[i].MCPServers = &servers
					}
				}
				if raw := value.Get("lspServers"); components && raw != nil {
					var servers protocol.JSONObject
					if servers.UnmarshalJSON(raw) == nil && servers.IsObject() {
						result.Enabled[i].LSPServers = &servers
					}
				}
			}
		}
	}
	update(l.cached)
	if l.fullPending != nil {
		update(&l.fullPending.result)
	}
	if l.cachedPending != nil {
		update(&l.cachedPending.result)
	}
}
