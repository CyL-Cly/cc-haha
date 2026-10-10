package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type pluginFixture struct {
	root           string
	store          *PluginStore
	bridge         *ManagedBridge
	catalog        Catalog
	mu             sync.Mutex
	policy         protocol.JSONObject
	reloadFailure  error
	reloadCalls    int
	deletedOptions []string
	foreignErrors  []string
}

func managedFixture(t *testing.T) *pluginFixture {
	t.Helper()
	root := t.TempDir()
	c, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	f := &pluginFixture{root: root, catalog: c, policy: record()}
	shared := storage.New(storage.Options{})
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, raw(record(field("unknown", record(field("keep", true))), field("enabledPlugins", record(field("foreign@market", false))))), 0600); err != nil {
		t.Fatal(err)
	}
	update := func(ctx context.Context, id string, value *bool) error {
		return shared.WithWriteLock(settingsPath, func() error {
			settings, err := readObject(settingsPath, record())
			if err != nil {
				return err
			}
			enabled := nested(settings.Get("enabledPlugins"))
			if value == nil {
				enabled.Delete(id)
			} else {
				enabled.Set(id, raw(*value))
			}
			settings.Set("enabledPlugins", raw(enabled))
			data, err := pretty(settings)
			if err != nil {
				return err
			}
			return os.WriteFile(settingsPath, data, 0600)
		})
	}
	o := PluginStoreOptions{ConfigDir: root, Store: shared, Now: func() time.Time { return time.UnixMilli(123456789) }, UUID: func() string { return "fixture" }, Policy: func(context.Context) (protocol.JSONObject, error) { return f.policy.Clone(), nil }, MarketplaceAllowed: func(context.Context, string) error { return nil }, UpdateEnabled: update, Reload: func(context.Context, string, *protocol.JSONObject) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reloadCalls++
		return f.reloadFailure
	}, DeleteOptions: func(_ context.Context, id string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.deletedOptions = append(f.deletedOptions, id)
		return nil
	}}
	o.Load = func(context.Context) (PluginLoadResult, error) {
		settings, err := readObject(settingsPath, record())
		if err != nil {
			return PluginLoadResult{}, err
		}
		enabled := nested(settings.Get("enabledPlugins"))
		installed, err := f.store.installed()
		if err != nil {
			return PluginLoadResult{}, err
		}
		result := PluginLoadResult{Errors: append([]string{}, f.foreignErrors...)}
		for _, p := range nested(installed.Get("plugins")).OwnProperties() {
			var id string
			_ = json.Unmarshal(p.NameJSON, &id)
			if string(enabled.Get(id)) != "true" {
				continue
			}
			var entries []protocol.JSONObject
			if err := json.Unmarshal(p.Value, &entries); err != nil {
				return result, err
			}
			for _, entry := range entries {
				if entry.String("scope") != "user" {
					continue
				}
				path := entry.String("installPath")
				manifest, err := readObject(filepath.Join(path, ".claude-plugin", "plugin.json"), record())
				if err != nil || manifest.String("name") == "" {
					result.Errors = append(result.Errors, id)
					continue
				}
				result.Enabled = append(result.Enabled, LoadedPlugin{id, path})
			}
		}
		return result, nil
	}
	f.store, err = NewPluginStore(o)
	if err != nil {
		t.Fatal(err)
	}
	f.bridge, err = NewManagedBridge(root, c, f.store, func() string { return "fixture" })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func defFor(t *testing.T, c Catalog, id string) protocol.JSONObject {
	t.Helper()
	for _, d := range c.Definitions {
		if d.String("id") == id {
			return d.Clone()
		}
	}
	t.Fatalf("missing fixture catalog id %s", id)
	return record()
}

func TestManagedBridgePublishesDisabledThenLoadsAndUninstallsOwnedFiles(t *testing.T) {
	f := managedFixture(t)
	def := defFor(t, f.catalog, "feishu")
	i := Installation{Directory: filepath.Join(f.root, "runtime"), Command: "/工具/O'Brien folder/cli", Args: []string{}, Environment: map[string]string{}}
	ctx := context.Background()
	if err := f.bridge.Install(ctx, def, i); err != nil {
		t.Fatal(err)
	}
	if ready, err := f.bridge.Ready(ctx, def); err != nil || ready {
		t.Fatalf("unauthenticated installation published ready: %v %v", ready, err)
	}
	cache := PluginCachePath(f.root, def.String("pluginId"), def.String("version")+"-connector.1")
	for _, file := range []string{filepath.Join(cache, ".claude-plugin", "plugin.json"), filepath.Join(cache, "skills", "office-feishu", "SKILL.md")} {
		if _, err := os.Stat(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.bridge.SetEnabled(ctx, def, true); err != nil {
		t.Fatal(err)
	}
	f.foreignErrors = []string{"unrelated@market"}
	if ready, err := f.bridge.Ready(ctx, def); err != nil || !ready {
		t.Fatalf("owned plugin should load independently of foreign error: %v %v", ready, err)
	}
	f.reloadFailure = errors.New("active tools did not reload")
	if err := f.bridge.Reload(ctx, "active", &def); err == nil {
		t.Fatal("disk publication substituted for real session reload")
	}
	if err := f.bridge.Remove(ctx, def); err != nil {
		t.Fatal(err)
	}
	market, err := readObject(filepath.Join(f.bridge.root, ".claude-plugin", "marketplace.json"), record())
	if err != nil || string(market.Get("plugins")) != "[]" {
		t.Fatalf("market removal: %s %v", raw(market), err)
	}
	settings, err := readObject(filepath.Join(f.root, "settings.json"), record())
	if err != nil {
		t.Fatal(err)
	}
	if settings.Get("unknown") == nil || nested(settings.Get("enabledPlugins")).Get("foreign@market") == nil || nested(settings.Get("enabledPlugins")).Get(def.String("pluginId")) != nil {
		t.Fatalf("unrelated settings lost or own entry left: %s", raw(settings))
	}
	marker, err := os.ReadFile(filepath.Join(cache, ".orphaned_at"))
	if err != nil || string(marker) != strconv.FormatInt(f.store.options.Now().UnixMilli(), 10) {
		t.Fatalf("original orphan marker: %q %v", marker, err)
	}
	if len(f.deletedOptions) != 1 || f.deletedOptions[0] != def.String("pluginId") {
		t.Fatalf("wrong credential namespace: %v", f.deletedOptions)
	}
}

func TestManagedBridgePreservesConcurrentMarketplaceMembers(t *testing.T) {
	f := managedFixture(t)
	defs := []protocol.JSONObject{defFor(t, f.catalog, "feishu"), defFor(t, f.catalog, "dingtalk"), defFor(t, f.catalog, "context7")}
	i := Installation{Directory: filepath.Join(f.root, "runtime"), Command: "cli", Args: []string{}, Environment: map[string]string{}}
	var wg sync.WaitGroup
	for _, def := range defs {
		wg.Add(1)
		go func(d protocol.JSONObject) {
			defer wg.Done()
			if err := f.bridge.Install(context.Background(), d, i); err != nil {
				t.Error(err)
			}
		}(def)
	}
	wg.Wait()
	market, err := readObject(filepath.Join(f.bridge.root, ".claude-plugin", "marketplace.json"), record())
	if err != nil {
		t.Fatal(err)
	}
	var entries []protocol.JSONObject
	if err = json.Unmarshal(market.Get("plugins"), &entries); err != nil || len(entries) != 3 {
		t.Fatalf("market members lost: %s %v", raw(market), err)
	}
	data, err := f.store.installed()
	if err != nil || len(nested(data.Get("plugins")).OwnProperties()) != 3 {
		t.Fatalf("installation members lost: %s %v", raw(data), err)
	}
}

func TestManagedBridgeRefusesSourceCollisionAndPolicyBeforePublishing(t *testing.T) {
	f := managedFixture(t)
	def := defFor(t, f.catalog, "feishu")
	path := filepath.Join(f.root, "plugins", "known_marketplaces.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	foreignSource := record(field("source", record(field("source", "directory"), field("path", "/foreign/source"))), field("installLocation", "/foreign/source"), field("lastUpdated", iso(time.Unix(0, 0))))
	source := raw(record(field(managedMarketplace, foreignSource)))
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.bridge.Install(context.Background(), def, Installation{}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("foreign marketplace was accepted: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(source) {
		t.Fatal("collision overwrote a user marketplace")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.policy = record(field("enabledPlugins", record(field(def.String("pluginId"), false))))
	if err := f.bridge.Install(context.Background(), def, Installation{}); err == nil {
		t.Fatal("organization force-disable was bypassed")
	}
	if _, err := os.Stat(f.bridge.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked installation published files: %v", err)
	}
}

func TestRemotePluginCacheRetainsSensitivePlaceholders(t *testing.T) {
	f := managedFixture(t)
	def := defFor(t, f.catalog, "amap")
	if err := f.bridge.Install(context.Background(), def, Installation{}); err != nil {
		t.Fatal(err)
	}
	cache := PluginCachePath(f.root, def.String("pluginId"), def.String("version")+"-connector.1")
	manifest, err := readObject(filepath.Join(cache, ".claude-plugin", "plugin.json"), record())
	if err != nil {
		t.Fatal(err)
	}
	apiKey := nested(nested(manifest.Get("userConfig")).Get("apiKey"))
	if string(apiKey.Get("sensitive")) != "true" || string(apiKey.Get("required")) != "true" {
		t.Fatal("key schema no longer sensitive/required")
	}
	mcp, err := os.ReadFile(filepath.Join(cache, ".mcp.json"))
	if err != nil || !strings.Contains(string(mcp), "${user_config.apiKey}") {
		t.Fatalf("credential marker lost: %s %v", mcp, err)
	}
	text, err := os.ReadFile(filepath.Join(cache, "skills", "office-amap", "SKILL.md"))
	if err != nil || strings.Contains(string(text), "apiKey") {
		t.Fatal("skill exposed credential configuration")
	}
}

func TestPluginStoreRetainsOtherScopesAndLegacyUserMetadata(t *testing.T) {
	f := managedFixture(t)
	def := defFor(t, f.catalog, "feishu")
	id := def.String("pluginId")
	path := filepath.Join(f.root, "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := record(field("version", 1), field("futureRoot", true), field("plugins", record(field("unrelated@market", record(field("version", "1.0.0"), field("installedAt", "old"), field("lastUpdated", "old"), field("installPath", "/old/cache"), field("future", true))))))
	if err := os.WriteFile(path, raw(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.bridge.Install(context.Background(), def, Installation{Command: "cli", Args: []string{}, Environment: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	data, err := f.store.installed()
	if err != nil {
		t.Fatal(err)
	}
	var foreign []protocol.JSONObject
	_ = json.Unmarshal(nested(data.Get("plugins")).Get("unrelated@market"), &foreign)
	if data.Get("futureRoot") == nil || len(foreign) != 1 || foreign[0].Get("future") == nil || foreign[0].String("scope") != "user" {
		t.Fatalf("legacy unknown metadata lost: %s", raw(data))
	}
	plugins := nested(data.Get("plugins"))
	var own []protocol.JSONObject
	_ = json.Unmarshal(plugins.Get(id), &own)
	own = append(own, record(field("scope", "project"), field("projectPath", "/project"), field("installPath", "/other/scope"), field("version", "1.0.0"), field("unknown", "keep")))
	plugins.Set(id, raw(own))
	data.Set("plugins", raw(plugins))
	if err := f.store.atomic(path, data); err != nil {
		t.Fatal(err)
	}
	if err := f.bridge.Remove(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	data, err = f.store.installed()
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(nested(data.Get("plugins")).Get(id), &own)
	if len(own) != 1 || own[0].String("scope") != "project" || own[0].String("unknown") != "keep" || len(f.deletedOptions) != 0 {
		t.Fatalf("other scope or credentials were removed: %s %v", raw(data), f.deletedOptions)
	}
}

func TestLocalCacheAndRegistryMatchActualSourcePersistence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	root := filepath.Join(config, "connectors", "marketplace")
	source := filepath.Join(root, "plugins", "office-fixture")
	clock, _ := time.Parse(time.RFC3339, "2026-10-06T00:00:00.000Z")
	for _, dir := range []string{filepath.Join(root, ".claude-plugin"), filepath.Join(source, ".claude-plugin"), filepath.Join(source, "skills", "office-fixture")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	version := "1.0.0-connector.1"
	entry := record(field("name", "office-fixture"), field("version", version), field("source", "./plugins/office-fixture"), field("strict", true))
	market := record(field("name", managedMarketplace), field("owner", record(field("name", "Claude Code Haha"))), field("plugins", []protocol.JSONObject{entry}))
	manifest := record(field("name", "office-fixture"), field("version", version), field("description", "fixture"), field("skills", "./skills"))
	skill := []byte("---\nname: fixture\n---\nRead this fixture.\n")
	for path, body := range map[string][]byte{filepath.Join(root, ".claude-plugin", "marketplace.json"): raw(market), filepath.Join(source, ".claude-plugin", "plugin.json"): raw(manifest), filepath.Join(source, "skills", "office-fixture", "SKILL.md"): skill} {
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	opts := PluginStoreOptions{ConfigDir: config, Store: storage.New(storage.Options{}), Now: func() time.Time { return clock }, UUID: func() string { return "fixture" }, Policy: func(context.Context) (protocol.JSONObject, error) { return record(), nil }, MarketplaceAllowed: func(context.Context, string) error { return nil }, UpdateEnabled: func(context.Context, string, *bool) error { return nil }, Load: func(context.Context) (PluginLoadResult, error) { return PluginLoadResult{}, nil }, Reload: func(context.Context, string, *protocol.JSONObject) error { return nil }, DeleteOptions: func(context.Context, string) error { return nil }}
	store, err := NewPluginStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RegisterMarketplace(ctx, root); err != nil {
		t.Fatal(err)
	}
	id := "office-fixture@haha-connectors"
	if err := store.CacheAndRegister(ctx, id, version, source); err != nil {
		t.Fatal(err)
	}
	before, err := store.installed()
	if err != nil {
		t.Fatal(err)
	}
	known, err := readObject(filepath.Join(config, "plugins", "known_marketplaces.json"), record())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UninstallUserPlugin(ctx, id); err != nil {
		t.Fatal(err)
	}
	after, err := store.installed()
	if err != nil {
		t.Fatal(err)
	}
	cache := PluginCachePath(config, id, version)
	marker, err := os.ReadFile(filepath.Join(cache, ".orphaned_at"))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(cache, "skills", "office-fixture", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	actual := raw(map[string]any{"known": known, "before": before, "after": after, "marker": string(marker), "skill": string(contents)})
	// Same private paths, recreated before original execution: no path or output
	// normalization is used for this persistence contract.
	if err := os.RemoveAll(config); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-connector-artifacts.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	child, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONNECTOR_ARTIFACT_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"store": map[string]any{"clock": iso(clock)}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source plugin store: %v %s", err, truncate(stderr.String(), 1200))
	}
	var a, b any
	if err := json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go local cache/registry differs from original: Go=%s Source=%s", truncate(string(actual), 1600), truncate(string(want), 1600))
	}
}
