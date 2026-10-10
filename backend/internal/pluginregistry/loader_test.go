package pluginregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type cacheFixture struct {
	home, cwd, market string
	options           Options
	input             map[string]any
	base              *protocol.JSONObject
}

// These are explicit settings/builtin producer bindings. Filesystem discovery,
// schema validation, policy, component loading and merge are production code.
func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	f := &cacheFixture{home: t.TempDir()}
	f.cwd = filepath.Join(f.home, "project")
	f.market = filepath.Join(f.home, "market")
	put(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{}`)
	put(t, filepath.Join(f.cwd, ".claude", "settings.local.json"), `{}`)
	canonical, err := filepath.EvalSymlinks(f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	f.cwd = canonical
	put(t, filepath.Join(f.home, "managed", "managed-settings.json"), `{}`)
	put(t, filepath.Join(f.home, ".claude", "settings.json"), `{"enabledPlugins":{"sample@mkt":true}}`)
	put(t, filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json"), string(raw(map[string]any{"mkt": map[string]any{"source": map[string]any{"source": "github", "repo": "company/market"}, "installLocation": f.market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})))
	f.catalog(t, []any{map[string]any{"name": "sample", "source": "./plugins/sample"}})
	f.plugin(t, filepath.Join(f.market, "plugins", "sample"), "sample", "")
	read := func(path string) protocol.JSONObject {
		b, err := os.ReadFile(path)
		if err != nil {
			return protocol.NewJSONObject()
		}
		return object(b)
	}
	policy := func(context.Context) (protocol.JSONObject, error) {
		return read(filepath.Join(f.home, "managed", "managed-settings.json")), nil
	}
	f.options = Options{
		HomeDir: f.home, ConfigDir: filepath.Join(f.home, ".claude"), CWD: f.cwd,
		Environment: func(string) string { return "" }, Builtins: NewBuiltinRegistry(),
		ClearSettings: func() { f.base = nil },
		Policy:        policy, AddDirEnabled: func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil },
		CacheSettings: func(_ context.Context, value protocol.JSONObject) error {
			copy := value.Clone()
			f.base = &copy
			return nil
		},
	}
	f.options.Installed, err = NewInstalledRegistry(InstalledOptions{PluginsDir: filepath.Join(f.home, ".claude", "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	f.options.Settings = func(ctx context.Context, project string) (protocol.JSONObject, error) {
		if project == "" {
			project = f.cwd
		}
		sources := []protocol.JSONObject{read(filepath.Join(f.home, ".claude", "settings.json")), read(filepath.Join(project, ".claude", "settings.json")), read(filepath.Join(project, ".claude", "settings.local.json"))}
		managed, _ := policy(ctx)
		sources = append(sources, managed)
		settings, enabled := protocol.NewJSONObject(), protocol.NewJSONObject()
		for _, source := range sources {
			for _, property := range source.OwnProperties() {
				_ = settings.SetPropertyJSON(property.NameJSON, property.Value)
			}
			for _, property := range object(source.Get("enabledPlugins")).OwnProperties() {
				_ = enabled.SetPropertyJSON(property.NameJSON, property.Value)
			}
		}
		settings.Set("enabledPlugins", raw(enabled))
		return settings, nil
	}
	f.input = map[string]any{"loadCache": true, "runtime": map[string]any{"cwd": f.cwd}}
	return f
}
func (f *cacheFixture) catalog(t *testing.T, plugins []any) {
	put(t, filepath.Join(f.market, ".claude-plugin", "marketplace.json"), string(raw(map[string]any{"name": "mkt", "owner": map[string]any{"name": "Owner"}, "plugins": plugins})))
}
func (f *cacheFixture) plugin(t *testing.T, path, name, dependencies string) {
	manifest := `{"name":"` + name + `","settings":{"agent":"Plan"}` + dependencies + `}`
	put(t, filepath.Join(path, ".claude-plugin", "plugin.json"), manifest)
	put(t, filepath.Join(path, "skills", "sample", "SKILL.md"), "# Real fixture skill")
}
func (f *cacheFixture) setting(t *testing.T, value string) {
	put(t, filepath.Join(f.home, ".claude", "settings.json"), value)
}
func (f *cacheFixture) installed(t *testing.T, plugins map[string]any) {
	put(t, filepath.Join(f.home, ".claude", "plugins", "installed_plugins.json"), string(raw(map[string]any{"version": 2, "plugins": plugins})))
}
func installEntry(scope, path, project string) map[string]any {
	entry := map[string]any{"scope": scope, "installPath": path, "version": "1.0.0", "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}
	if project != "" {
		entry["projectPath"] = project
	}
	return entry
}

func TestCachedPluginLoadingAgainstProductionSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned Source Bun not configured")
	}
	scenarios := map[string]func(*testing.T, *cacheFixture){
		"local-directory": func(*testing.T, *cacheFixture) {},
		"catalog-file": func(t *testing.T, f *cacheFixture) {
			b, _ := os.ReadFile(filepath.Join(f.market, ".claude-plugin", "marketplace.json"))
			path := filepath.Join(f.market, "catalog.json")
			put(t, path, string(b))
			put(t, filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json"), string(raw(map[string]any{"mkt": map[string]any{"source": map[string]any{"source": "file", "path": path}, "installLocation": path, "lastUpdated": "2026-10-01T00:00:00.000Z"}})))
		},
		"manifest-supplement": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": "./plugins/sample", "commands": []string{"./extra.md", "./missing.md"}, "skills": "./extra-skills", "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "offline"}}}}}}})
			put(t, filepath.Join(f.market, "plugins", "sample", "extra.md"), "Actual command")
			put(t, filepath.Join(f.market, "plugins", "sample", "extra-skills", "SKILL.md"), "Actual skill")
		},
		"marketplace-only-manifest": func(t *testing.T, f *cacheFixture) {
			if err := os.Remove(filepath.Join(f.market, "plugins", "sample", ".claude-plugin", "plugin.json")); err != nil {
				t.Fatal(err)
			}
			f.catalog(t, []any{map[string]any{"name": "sample", "source": "./plugins/sample", "description": "Market description", "commands": map[string]any{"inline": map[string]any{"content": "Not loaded as marketplace command"}, "file": map[string]any{"source": "./extra.md"}}, "skills": "./extra-skills", "workflows": "./workflows", "mcpServers": map[string]any{"one": map[string]any{"command": "offline"}}}})
			put(t, filepath.Join(f.market, "plugins", "sample", "extra.md"), "Command")
			put(t, filepath.Join(f.market, "plugins", "sample", "extra-skills", "SKILL.md"), "Skill")
		},
		"disabled-and-missing": func(t *testing.T, f *cacheFixture) {
			f.setting(t, `{"enabledPlugins":{"sample@mkt":false,"delisted@mkt":false,"missing@mkt":true,"bad-id":true}}`)
		},
		"external-cache": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "owner/plugin", "sha": "abcdef"}}})
			path := filepath.Join(f.home, "external")
			f.plugin(t, path, "sample", "")
			f.installed(t, map[string]any{"sample@mkt": []any{installEntry("user", path, "")}})
		},
		"external-cache-miss": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "owner/plugin"}}})
		},
		"installed-without-version": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "owner/plugin"}}})
			path := filepath.Join(f.home, "external")
			f.plugin(t, path, "sample", "")
			entry := installEntry("user", path, "")
			delete(entry, "version")
			f.installed(t, map[string]any{"sample@mkt": []any{entry}})
		},
		"strict-manifest-conflict": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": "./plugins/sample", "strict": false, "skills": "./skills"}})
		},
		"builtins-defaults-and-replacement": func(t *testing.T, f *cacheFixture) {
			disabled, emptyVersion := false, ""
			f.options.Builtins.Register(BuiltinDefinition{Name: "first", Description: "Old"})
			f.options.Builtins.Register(BuiltinDefinition{Name: "second", Description: "Disabled", DefaultEnabled: &disabled})
			f.options.Builtins.Register(BuiltinDefinition{Name: "hidden", Description: "Hidden", Available: func() bool { return false }})
			f.options.Builtins.Register(BuiltinDefinition{Name: "first", Description: "Replacement", Version: &emptyVersion})
			f.input["runtime"].(map[string]any)["builtins"] = []any{map[string]any{"name": "first", "description": "Old"}, map[string]any{"name": "second", "description": "Disabled", "defaultEnabled": false}, map[string]any{"name": "hidden", "description": "Hidden", "available": false}, map[string]any{"name": "first", "description": "Replacement", "version": ""}}
		},
		"additional-directory-lowest-priority": func(t *testing.T, f *cacheFixture) {
			path := filepath.Join(f.home, "additional")
			put(t, filepath.Join(path, ".claude", "settings.json"), `{"enabledPlugins":{"sample@mkt":false,"missing@mkt":true}}`)
			f.options.AddDirEnabled = func(context.Context) (protocol.JSONObject, error) {
				return object(json.RawMessage(`{"sample@mkt":false,"missing@mkt":true}`)), nil
			}
			f.input["runtime"].(map[string]any)["additionalDirs"] = []string{path}
		},
		"malformed-known-without-policy": func(t *testing.T, f *cacheFixture) {
			put(t, filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json"), string(raw(map[string]any{"mkt": map[string]any{"installLocation": f.market}})))
		},
		"unknown-source-denied": func(t *testing.T, f *cacheFixture) {
			put(t, filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json"), string(raw(map[string]any{"mkt": map[string]any{"installLocation": f.market}})))
			put(t, filepath.Join(f.home, "managed", "managed-settings.json"), `{"strictKnownMarketplaces":[]}`)
		},
		"blocklist-git-equivalence": func(t *testing.T, f *cacheFixture) {
			put(t, filepath.Join(f.home, "managed", "managed-settings.json"), `{"blockedMarketplaces":[{"source":"git","url":"https://github.com/company/market.git"}]}`)
		},
		"inline-overrides": func(t *testing.T, f *cacheFixture) {
			path := filepath.Join(f.cwd, "inline")
			f.plugin(t, path, "sample", "")
			f.options.InlinePaths = []string{"inline"}
			f.input["runtime"].(map[string]any)["inlinePaths"] = []string{"inline"}
		},
		"managed-lock": func(t *testing.T, f *cacheFixture) {
			path := filepath.Join(f.cwd, "inline")
			f.plugin(t, path, "sample", "")
			f.options.InlinePaths = []string{path}
			f.input["runtime"].(map[string]any)["inlinePaths"] = []string{path}
			put(t, filepath.Join(f.home, "managed", "managed-settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
		},
		"project-precedence-and-demotion": func(t *testing.T, f *cacheFixture) {
			f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "owner/plugin"}}, map[string]any{"name": "dependent", "source": "./plugins/dependent"}})
			f.plugin(t, filepath.Join(f.market, "plugins", "dependent"), "dependent", `,"dependencies":["sample"]`)
			paths := []string{filepath.Join(f.home, "user"), filepath.Join(f.home, "local"), filepath.Join(f.home, "other")}
			for _, path := range paths {
				f.plugin(t, path, "sample", "")
			}
			f.installed(t, map[string]any{"sample@mkt": []any{installEntry("project", paths[2], filepath.Join(f.home, "other-project")), installEntry("user", paths[0], ""), installEntry("local", paths[1], f.cwd)}, "dependent@mkt": []any{installEntry("user", filepath.Join(f.market, "plugins", "dependent"), "")}})
			put(t, filepath.Join(f.cwd, ".claude", "settings.local.json"), `{"enabledPlugins":{"sample@mkt":false,"dependent@mkt":true}}`)
			f.input["runtime"].(map[string]any)["project"] = f.cwd
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			f := newCacheFixture(t)
			setup(t, f)
			var expected struct {
				Cache          json.RawMessage
				PluginSettings json.RawMessage
			}
			if err := json.Unmarshal(source(t, f.home, f.input), &expected); err != nil {
				t.Fatal(err)
			}
			loader, err := New(f.options)
			if err != nil {
				t.Fatal(err)
			}
			project, _ := f.input["runtime"].(map[string]any)["project"].(string)
			var result Result
			if project == "" {
				result, err = loader.LoadCached(context.Background())
			} else {
				result, err = loader.LoadProject(context.Background(), project)
			}
			if err != nil {
				t.Fatal(err)
			}
			compare(t, result, expected.Cache)
			var base any
			if f.base != nil && len(f.base.OwnProperties()) > 0 {
				base = f.base
			}
			compare(t, base, expected.PluginSettings)
		})
	}
}

func TestCachedLoaderInvalidationAndDependencyDemotion(t *testing.T) {
	f := newCacheFixture(t)
	f.plugin(t, filepath.Join(f.market, "plugins", "sample"), "sample", `,"dependencies":["missing"]`)
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loader.LoadCached(context.Background())
	if err != nil || len(result.Enabled) != 0 || len(result.Disabled) != 1 || result.Disabled[0].Enabled || len(result.Errors) != 1 {
		t.Fatalf("initial=%+v err=%v", result, err)
	}
	f.plugin(t, filepath.Join(f.market, "plugins", "sample"), "sample", "")
	stale, err := loader.LoadCached(context.Background())
	if err != nil || len(stale.Enabled) != 0 {
		t.Fatalf("memoization lost: %+v %v", stale, err)
	}
	loader.Clear()
	fresh, err := loader.LoadCached(context.Background())
	if err != nil || len(fresh.Enabled) != 1 || len(fresh.Errors) != 0 || f.base.String("agent") != "Plan" {
		t.Fatalf("refresh=%+v %v", fresh, err)
	}
	if _, err := loader.LoadFull(context.Background()); err == nil {
		t.Fatal("cache-only result satisfied full materialization without producer")
	}
}

func TestConcurrentCachedLoadPublishesOneSettingsSnapshot(t *testing.T) {
	f := newCacheFixture(t)
	count := 0
	f.options.CacheSettings = func(context.Context, protocol.JSONObject) error { count++; return nil }
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := loader.LoadCached(context.Background())
			if err != nil || len(result.Enabled) != 1 {
				t.Errorf("load=%+v %v", result, err)
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("settings published %d times", count)
	}
}

func TestInvalidInstalledVersionCannotSelectAParentCacheDirectory(t *testing.T) {
	f := newCacheFixture(t)
	f.catalog(t, []any{map[string]any{"name": "sample", "source": map[string]any{"source": "github", "repo": "owner/plugin"}}})
	path := filepath.Join(f.home, "external")
	f.plugin(t, path, "sample", "")
	entry := installEntry("user", path, "")
	delete(entry, "version")
	f.installed(t, map[string]any{"sample@mkt": []any{entry}})
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loader.LoadCached(context.Background())
	if err != nil || len(result.Enabled) != 0 || len(result.Errors) != 1 || result.Errors[0].String("type") != "plugin-cache-miss" || result.Errors[0].String("installPath") != "(not recorded)" {
		t.Fatalf("invalid registry selected a plugin: %+v %v", result, err)
	}
}

type deniedCatalogFS struct {
	OSFileSystem
	nested, fallback string
	readFallback     bool
}

func (f *deniedCatalogFS) Stat(path string) (fs.FileInfo, error) {
	if path == f.nested {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrPermission}
	}
	return f.OSFileSystem.Stat(path)
}

func (f *deniedCatalogFS) ReadFile(path string) ([]byte, error) {
	if path == f.nested {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	if path == f.fallback {
		f.readFallback = true
	}
	return f.OSFileSystem.ReadFile(path)
}
func TestInaccessibleNestedCatalogDoesNotFallBackToAnotherSource(t *testing.T) {
	f := newCacheFixture(t)
	filesystem := &deniedCatalogFS{nested: filepath.Join(f.market, ".claude-plugin", "marketplace.json"), fallback: f.market}
	f.options.Files = filesystem
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	if loader.marketplace(f.market) != nil || filesystem.readFallback {
		t.Fatal("permission failure fell through to a different marketplace file")
	}
}

func TestFullRefreshIsFreshAndWarmsOnlyTheCachedConsumer(t *testing.T) {
	f := newCacheFixture(t)
	f.catalog(t, []any{map[string]any{"name": "missing", "source": map[string]any{"source": "github", "repo": "owner/missing"}}, map[string]any{"name": "sample", "source": "./plugins/sample"}})
	f.setting(t, `{"enabledPlugins":{"missing@mkt":true,"sample@mkt":true}}`)
	count := 0
	f.options.Prepare = func(_ context.Context, _ protocol.JSONObject, _, id string, _ protocol.JSONObject) (Prepared, error) {
		count++
		if id == "missing@mkt" {
			return Prepared{}, errors.New("materialization failed")
		}
		return Prepared{Path: filepath.Join(f.market, "plugins", "sample")}, nil
	}
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loader.LoadFull(context.Background())
	if err != nil || len(result.Enabled) != 1 || result.Enabled[0].Source != "sample@mkt" || len(result.Errors) != 1 {
		t.Fatalf("one failure contaminated the next plugin: %+v %v", result, err)
	}
	_, err = loader.LoadCached(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("full result did not warm cache: calls=%d %v", count, err)
	}
	_, err = loader.LoadFull(context.Background())
	if err != nil || count != 4 {
		t.Fatalf("cached result satisfied refresh: calls=%d %v", count, err)
	}
}
