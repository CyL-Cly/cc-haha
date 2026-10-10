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
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cached == nil || snapshot.cacheID == 0 || snapshot.cacheID != l.cached.cacheID {
		return
	}
	for i, plugin := range l.cached.Enabled {
		for _, value := range objects {
			if value.String("source") != plugin.Source || value.String("path") != plugin.Path {
				continue
			}
			if raw := value.Get("mcpServers"); raw != nil {
				var servers protocol.JSONObject
				if servers.UnmarshalJSON(raw) == nil && servers.IsObject() {
					l.cached.Enabled[i].MCPServers = &servers
				}
			}
		}
	}
}
