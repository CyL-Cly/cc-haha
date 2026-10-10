package pluginregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestPortableRebasePreservesUserExtensionFieldsAndSessionSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	expected := VersionedPathIn(dir, "sample@mkt", "1.0")
	put(t, filepath.Join(expected, "SKILL.md"), "Fixture")
	file := filepath.Join(dir, "installed_plugins.json")
	put(t, file, `{"version":2,"futureRoot":{"keep":true},"plugins":{"sample@mkt":[{"scope":"user","version":"1.0","installPath":"/old/home/cache","futureEntry":{"raw":"keep"}}]}}`)
	r, err := NewInstalledRegistry(InstalledOptions{PluginsDir: dir, Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	session, err := r.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := object(array(object(session.Get("plugins")).Get("sample@mkt"))[0])
	if entry.String("installPath") != expected || entry.Get("futureEntry") != nil {
		t.Fatalf("runtime schema projection: %s", raw(entry))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	persisted := object(b)
	preserved := object(array(object(persisted.Get("plugins")).Get("sample@mkt"))[0])
	if persisted.Get("futureRoot") == nil || preserved.Get("futureEntry") == nil || preserved.String("installPath") != expected {
		t.Fatalf("unknown fields lost on rebase: %s", b)
	}
	if len(b) == 0 || b[len(b)-1] == '\n' {
		t.Fatal("installed registry gained an unrelated trailing newline")
	}
}
func TestLegacyInstalledMigrationWritesV2OnlyOnManagementAndRetainsUnknownFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	file := filepath.Join(dir, "installed_plugins.json")
	old := `{"version":1.0,"futureRoot":42,"plugins":{"sample@mkt":{"version":"1","installedAt":"2026-01-01","installPath":"/legacy","futureEntry":"keep"}}}`
	put(t, file, old)
	r, err := NewInstalledRegistry(InstalledOptions{PluginsDir: dir, Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := object(array(object(data.Get("plugins")).Get("sample@mkt"))[0])
	if entry.String("scope") != "user" || entry.String("installPath") != VersionedPathIn(dir, "sample@mkt", "1") {
		t.Fatalf("legacy projection=%s", raw(data))
	}
	before, _ := os.ReadFile(file)
	if string(before) != old {
		t.Fatal("read-only V1 conversion mutated the original file")
	}
	metadata := protocol.NewJSONObject(field("version", "2"), field("installedAt", "2026-02-01"))
	if err := r.Add(context.Background(), "other@mkt", "user", "/other/cache", metadata, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	stored := object(b)
	oldEntry := object(array(object(stored.Get("plugins")).Get("sample@mkt"))[0])
	if !numericVersion(stored.Get("version"), 2) || stored.Get("futureRoot") == nil || oldEntry.String("futureEntry") != "keep" {
		t.Fatalf("V2 upgrade discarded original state: %s", b)
	}
}
func TestSharedInstalledTransactionDoesNotLoseConcurrentScopes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	store := storage.New(storage.Options{})
	put(t, filepath.Join(dir, "installed_plugins.json"), `{"version":2,"futureRoot":"keep","plugins":{}}`)
	a, _ := NewInstalledRegistry(InstalledOptions{PluginsDir: dir, Store: store})
	b, _ := NewInstalledRegistry(InstalledOptions{PluginsDir: dir, Store: store})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := "project"
			r := a
			if i%2 == 1 {
				scope = "local"
				r = b
			}
			project := filepath.Join(dir, string(rune('a'+i)))
			err := r.Add(context.Background(), "sample@mkt", scope, "/cache", protocol.NewJSONObject(field("version", "1")), &project)
			if err != nil {
				t.Errorf("write=%v", err)
			}
		}(i)
	}
	wg.Wait()
	data, err := a.Disk()
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	_ = json.Unmarshal(object(data.Get("plugins")).Get("sample@mkt"), &entries)
	if len(entries) != 20 {
		t.Fatalf("lost concurrent scope: %d", len(entries))
	}
	bytes, _ := os.ReadFile(filepath.Join(dir, "installed_plugins.json"))
	if object(bytes).String("futureRoot") != "keep" {
		t.Fatal("concurrent transaction discarded extension state")
	}
}
