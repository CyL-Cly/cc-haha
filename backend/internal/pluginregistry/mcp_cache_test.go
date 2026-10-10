package pluginregistry

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMCPCacheUsesOriginalLoadLifetimeAndImmutableStructSnapshots(t *testing.T) {
	f := newCacheFixture(t)
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	old, err := loader.LoadCached(context.Background())
	if err != nil || len(old.Enabled) == 0 {
		t.Fatal(old, err)
	}
	plugin := old.Enabled[0]
	value := protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: raw(plugin.Source)}, protocol.JSONField{Name: "path", Value: raw(plugin.Path)}, protocol.JSONField{Name: "mcpServers", Value: json.RawMessage(`{"peer":{"command":"${UNEXPANDED}"}}`)})
	loader.PublishMCPCache(old, []protocol.JSONObject{value})
	next, err := loader.LoadCached(context.Background())
	if err != nil || next.Enabled[0].MCPServers == nil || next.Enabled[0].MCPServers.String("peer") == "unexpected" {
		t.Fatal(next, err)
	}
	if old.Enabled[0].MCPServers != nil {
		t.Fatal("old native struct snapshot changed")
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 20; j++ {
				snapshot, e := loader.LoadCached(context.Background())
				if e != nil {
					t.Error(e)
					return
				}
				_ = snapshot.Enabled[0].MCPServers.Get("peer")
				loader.PublishMCPCache(old, []protocol.JSONObject{value})
			}
		}()
	}
	group.Wait()
	loader.Clear()
	fresh, err := loader.LoadCached(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	loader.PublishMCPCache(old, []protocol.JSONObject{value})
	cached, err := loader.LoadCached(context.Background())
	if err != nil || fresh.Enabled[0].MCPServers != nil || cached.Enabled[0].MCPServers != nil {
		t.Fatal("stale extraction published after refresh", fresh, cached, err)
	}
}
