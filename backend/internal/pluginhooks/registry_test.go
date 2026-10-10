package pluginhooks

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"sync"
	"testing"
)

func TestRegistryKeepsCallbacksAndOwnsConcurrentSnapshots(t *testing.T) {
	parse := func(input string) *protocol.JSONObject {
		t.Helper()
		var value protocol.JSONObject
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatal(err)
		}
		return &value
	}
	registry := New()
	registry.Register(*parse(`{"Stop":[{"matcher":"callback","hooks":[]}]}`))
	plugin := pluginregistry.Loaded{Name: "sample", Source: "sample@mkt", Path: "/owned/plugin", Enabled: true, HooksConfig: parse(`{"Stop":[{"hooks":[{"type":"command","command":"fixture-command"}]}]}`)}
	registry.LoadPlugins([]pluginregistry.Loaded{plugin})
	snapshot := registry.Snapshot()
	if snapshot == nil || !strings.Contains(string(snapshot.Get("Stop")), "fixture-command") {
		t.Fatal("actual plugin metadata absent")
	}
	snapshot.Set("Stop", raw([]any{}))
	if !strings.Contains(string(registry.Snapshot().Get("Stop")), "fixture-command") {
		t.Fatal("borrowed snapshot mutated registry")
	}
	var group sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for turn := 0; turn < 20; turn++ {
				registry.Clear()
				registry.Prune([]pluginregistry.Loaded{plugin})
				registry.LoadPlugins([]pluginregistry.Loaded{plugin})
				current := registry.Snapshot()
				if current == nil || !strings.Contains(string(current.Get("Stop")), "callback") {
					t.Error("swap lost caller callbacks")
				}
			}
		}()
	}
	group.Wait()
	registry.Prune(nil)
	current := registry.Snapshot()
	if current == nil || strings.Contains(string(current.Get("Stop")), "fixture-command") || !strings.Contains(string(current.Get("Stop")), "callback") {
		t.Fatal("prune retained removed plugins or removed callbacks", current)
	}
}
