package pluginregistry

import (
	"context"
	"encoding/json"
	"path/filepath"
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

func TestComponentCacheSharesFullPromiseAndRejectsPreviousLoad(t *testing.T) {
	f := newCacheFixture(t)
	f.options.Prepare = func(context.Context, protocol.JSONObject, string, string, protocol.JSONObject) (Prepared, error) {
		return Prepared{Path: filepath.Join(f.market, "plugins", "sample")}, nil
	}
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := loader.LoadFull(context.Background())
	if err != nil || len(initial.Enabled) != 1 {
		t.Fatal(initial, err)
	}
	plugin := initial.Enabled[0]
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "source", Value: raw(plugin.Source)}, protocol.JSONField{Name: "path", Value: raw(plugin.Path)}, protocol.JSONField{Name: "lspServers", Value: json.RawMessage(`{"owned":{"command":"private-fixture"}}`)})
	hydrated := initial
	hydrated.Errors = append(append([]protocol.JSONObject{}, initial.Errors...), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("lsp-config-invalid")}, protocol.JSONField{Name: "source", Value: raw("plugin")}))
	loader.PublishComponentCache(hydrated, []protocol.JSONObject{fields})
	for _, load := range []func(context.Context) (Result, error){loader.LoadCached, loader.LoadFull} {
		current, err := load(context.Background())
		if err != nil || current.Enabled[0].LSPServers == nil || current.Enabled[0].LSPServers.Get("owned") == nil || len(current.Errors) != len(initial.Errors)+1 {
			t.Fatal("lazy component/error mutation was not visible through the original load promise", current, err)
		}
	}
	if initial.Enabled[0].LSPServers != nil || len(initial.Errors) != 0 {
		t.Fatal("returned immutable native snapshot was changed")
	}
	loader.Clear()
	if _, err = loader.LoadFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	loader.PublishComponentCache(hydrated, []protocol.JSONObject{fields})
	fresh, err := loader.LoadFull(context.Background())
	if err != nil || fresh.Enabled[0].LSPServers != nil || len(fresh.Errors) != 0 {
		t.Fatal("late component callback crossed the original load identity", fresh, err)
	}
}
