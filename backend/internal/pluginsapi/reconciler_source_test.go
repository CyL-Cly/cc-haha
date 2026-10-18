package pluginsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func sourcePluginReconciliation(t *testing.T, home string, input map[string]any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun unavailable")
	}
	input["home"] = home
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plugin-reconciliation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=" + filepath.Join(home, "bin"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_RECONCILIATION_SOURCE=1"}
	command.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("actual plugin Source %v\n%s\n%s", err, output, stderr.String())
	}
	var result protocol.JSONObject
	if err := result.UnmarshalJSON(output); err != nil {
		t.Fatal(err, string(output))
	}
	return result
}

func marketplaceReconciliationFixture(t *testing.T) (*fixture, *MarketplaceManager) {
	t.Helper()
	f := newFixture(t)
	f.workspace, _ = filepath.EvalSymlinks(f.workspace)
	_ = os.MkdirAll(filepath.Join(f.home, "tmp"), 0700)
	_ = os.WriteFile(filepath.Join(f.config, ".claude.json"), []byte(`{}`), 0600)
	resolver, err := NewMarketplaceResolver(MarketplaceOptions{Home: f.home, WorkDir: f.workspace, PluginsDir: filepath.Join(f.config, "plugins"), Environment: map[string]string{"HOME": f.home, "PATH": os.Getenv("PATH")}, Store: storage.New(storage.Options{}), Now: func() time.Time { return time.UnixMilli(1791244800000) }})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewMarketplaceManager(MarketplaceManagerOptions{Resolver: resolver, ReadSettings: f.service.options.ReadSettings, WriteSettings: func(context.Context, string, protocol.JSONObject, [][]string) error {
		return fmt.Errorf("reconcile must not declare")
	}, ClearCaches: func(context.Context) error { return fmt.Errorf("reconcile must not clear callers' caches") }})
	if err != nil {
		t.Fatal(err)
	}
	return f, manager
}

func TestMarketplaceReconcilerActualSourceDiffAndMaterialization(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"from-url","owner":{"name":"Owner"},"plugins":[]}`))
	}))
	defer peer.Close()
	for _, profile := range []string{"missing-directory", "missing-local-fails", "changed-local", "changed-dead-preserves", "fallback-present", "settings-source", "zip-skip-local", "url-loopback"} {
		t.Run(profile, func(t *testing.T) {
			f, manager := marketplaceReconciliationFixture(t)
			market := filepath.Join(f.workspace, "market")
			_ = os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700)
			_ = os.WriteFile(filepath.Join(market, ".claude-plugin", "marketplace.json"), []byte(`{"name":"mkt","owner":{"name":"Owner"},"plugins":[]}`), 0600)
			source := rec(field("source", "directory"), field("path", "./market"))
			declared := rec(field("mkt", rec(field("source", source))))
			known := rec()
			switch profile {
			case "missing-local-fails":
				source.Set("path", raw("./missing"))
				declared.Set("mkt", raw(rec(field("source", source))))
			case "changed-local", "changed-dead-preserves":
				known.Set("mkt", raw(rec(field("source", rec(field("source", "directory"), field("path", filepath.Join(f.home, "old")))), field("installLocation", filepath.Join(f.home, "old")), field("lastUpdated", "2026-10-01T00:00:00.000Z"))))
				if profile == "changed-dead-preserves" {
					source.Set("path", raw("./missing"))
					declared.Set("mkt", raw(rec(field("source", source))))
				}
			case "fallback-present":
				declared = rec(field("claude-plugins-official", rec(field("source", rec(field("source", "github"), field("repo", "anthropics/claude-plugins-official"))), field("sourceIsFallback", true))))
				known.Set("claude-plugins-official", raw(rec(field("source", rec(field("source", "directory"), field("path", market))), field("installLocation", market), field("lastUpdated", "2026-10-01T00:00:00.000Z"))))
			case "settings-source":
				source = rec(field("source", "settings"), field("name", "mkt"), field("plugins", []any{}))
				declared.Set("mkt", raw(rec(field("source", source))))
			case "url-loopback":
				source = rec(field("source", "url"), field("url", peer.URL))
				declared = rec(field("from-url", rec(field("source", source))))
			}
			_ = os.MkdirAll(filepath.Join(f.config, "plugins"), 0700)
			_ = os.WriteFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"), raw(known), 0600)
			settings := rec(field("futureField", rec(field("keep", true))), field("extraKnownMarketplaces", declared))
			if profile == "fallback-present" {
				settings = rec(field("enabledPlugins", rec(field("sample@claude-plugins-official", true))))
			}
			original := raw(settings)
			_ = os.WriteFile(filepath.Join(f.config, "settings.json"), original, 0600)
			input := map[string]any{"operation": "reconcile", "url": peer.URL, "zip": profile == "zip-skip-local"}
			expected := sourcePluginReconciliation(t, f.home, input)
			// Restore only the Source-mutated materialized mapping. The native
			// invocation must independently fetch/copy and produce its progress.
			_ = os.WriteFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"), raw(known), 0600)
			diff := DiffMarketplaces(declared, known, f.workspace)
			observations := []ReconcileProgress{}
			options := ReconcileOptions{OnProgress: func(event ReconcileProgress) { observations = append(observations, event) }}
			if profile == "zip-skip-local" {
				options.Skip = func(_ string, source protocol.JSONObject) bool {
					return source.String("source") == "file" || source.String("source") == "directory"
				}
			}
			result, err := manager.Reconcile(context.Background(), declared, f.workspace, options)
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]any{"diff": diff, "result": result, "observations": observations} {
				var goValue, sourceValue any
				_ = json.Unmarshal(raw(value), &goValue)
				_ = json.Unmarshal(expected.Get(key), &sourceValue)
				if !reflect.DeepEqual(goValue, sourceValue) {
					t.Fatalf("%s differs\nGo=%s\nSource=%s", key, raw(value), expected.Get(key))
				}
			}
			actual, err := manager.Known(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !equalJSON(actual, obj(expected.Get("known"))) {
				t.Fatal("real mapping differs", string(raw(actual)), string(expected.Get("known")))
			}
			persisted, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if !bytes.Equal(persisted, original) {
				t.Fatal("startup wrote declared settings", string(persisted))
			}
		})
	}
}

func TestMarketplaceDiffActualSourceCanonicalWorktreeAndPropertyOrder(t *testing.T) {
	f, _ := marketplaceReconciliationFixture(t)
	main := filepath.Join(f.home, "main")
	tree := filepath.Join(f.home, "tree")
	gitdir := filepath.Join(main, ".git", "worktrees", "tree")
	_ = os.MkdirAll(gitdir, 0700)
	_ = os.MkdirAll(tree, 0700)
	for path, content := range map[string]string{filepath.Join(tree, ".git"): "gitdir: " + gitdir + "\n", filepath.Join(gitdir, "commondir"): "../..\n", filepath.Join(gitdir, "gitdir"): filepath.Join(tree, ".git") + "\n"} {
		_ = os.WriteFile(path, []byte(content), 0600)
	}
	declared := rec(field("10", rec(field("source", rec(field("source", "file"), field("path", "./market.json"))))), field("2", rec(field("source", rec(field("source", "directory"), field("path", "../other"))))), field("changed", rec(field("source", rec(field("source", "url"), field("url", "https://fixture.invalid/a"), field("headers", map[string]string{"A": "x"}))))))
	known := rec(field("10", rec(field("source", rec(field("source", "file"), field("path", filepath.Join(main, "market.json")))))))
	expected := sourcePluginReconciliation(t, f.home, map[string]any{"operation": "diff", "cwd": tree, "declared": declared, "known": known})
	actual := DiffMarketplaces(declared, known, tree)
	if !strings.Contains(string(expected.Get("diff")), `"upToDate":["10"]`) {
		t.Fatal("Source canonical caller invalid", string(expected.Get("diff")))
	}
	if string(raw(actual)) != string(expected.Get("diff")) {
		t.Fatal("canonical Source diff", string(raw(actual)), string(expected.Get("diff")))
	}
}

func TestHeadlessPluginDelistingActualSourceLegacyRegistryAndFlaggedLifetime(t *testing.T) {
	for _, profile := range []string{"editable-delisted", "managed-only", "already-flagged", "expired-seen", "force-disabled", "legacy-v1"} {
		t.Run(profile, func(t *testing.T) {
			seed := func() (*fixture, *MarketplaceManager) {
				f, manager := marketplaceReconciliationFixture(t)
				scope := "user"
				if profile == "managed-only" {
					scope = "managed"
				}
				files := seedPlugin("gone@mkt", scope, "", "1.0.0")
				files = append(files, map[string]any{"path": "$HOME/market/.claude-plugin/marketplace.json", "content": string(raw(map[string]any{"name": "mkt", "owner": map[string]string{"name": "Owner"}, "plugins": []any{}, "forceRemoveDeletedPlugins": profile != "force-disabled"}))})
				if profile == "already-flagged" || profile == "expired-seen" {
					entry := map[string]any{"flaggedAt": "2026-10-01T00:00:00.000Z", "futureEntry": true}
					if profile == "expired-seen" {
						entry["seenAt"] = "2026-10-03T00:00:00.000Z"
					}
					files = append(files, map[string]any{"path": "$CONFIG/plugins/flagged-plugins.json", "content": string(raw(map[string]any{"futureFlagged": true, "plugins": map[string]any{"gone@mkt": entry}}))})
				}
				if profile == "legacy-v1" {
					files = append(files, map[string]any{"path": "$CONFIG/plugins/installed_plugins.json", "content": `{"version":1,"plugins":{"gone@mkt":{"version":"1.0.0","installedAt":"2026-10-01T00:00:00.000Z","lastUpdated":"2026-10-01T00:00:00.000Z","futureInstallation":{"keep":true}}}}`})
				}
				f.seed(t, map[string]any{"reset": true, "settings": map[string]any{"futureUser": map[string]bool{"keep": true}, "enabledPlugins": map[string]bool{"gone@mkt": true, "keep@mkt": false}}, "files": files})
				return f, manager
			}
			source, _ := seed()
			expected := sourcePluginReconciliation(t, source.home, map[string]any{"operation": "headless"})
			f, manager := seed()
			newly, err := manager.EnforceDelisting(context.Background(), f.service, time.UnixMilli(1791244800000))
			if err != nil {
				t.Fatal(err)
			}
			changed := len(newly) > 0
			if string(raw(changed)) != string(expected.Get("result")) {
				t.Fatal("headless delisting changed result", changed, string(expected.Get("result")))
			}
			actual := map[string]any{}
			for _, name := range []string{"settings.json", "plugins/installed_plugins.json", "plugins/flagged-plugins.json"} {
				data, err := os.ReadFile(filepath.Join(f.config, name))
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				var value any
				_ = json.Unmarshal(data, &value)
				actual[name] = value
			}
			var sourceFiles any
			_ = json.Unmarshal(expected.Get("files"), &sourceFiles)
			nativeNormalized := normalize(actual, f.home)
			sourceNormalized := normalize(sourceFiles, source.home)
			if !reflect.DeepEqual(nativeNormalized, sourceNormalized) {
				t.Fatalf("actual persisted delisting effects\nGo=%s\nSource=%s", raw(nativeNormalized), raw(sourceNormalized))
			}
		})
	}
}

func TestHeadlessPluginActualSourceSeedPrecedenceFallbackAndZipSteadyState(t *testing.T) {
	for _, profile := range []string{"first-valid-seed-wins", "missing-first-seed-falls-through", "official-fallback-preserves-seed", "zip-steady-state"} {
		t.Run(profile, func(t *testing.T) {
			f, manager := marketplaceReconciliationFixture(t)
			seeds := []string{filepath.Join(f.home, "seed-one"), filepath.Join(f.home, "seed-two")}
			name := "seed-market"
			if profile == "official-fallback-preserves-seed" {
				name = "claude-plugins-official"
			}
			for index, seed := range seeds {
				_ = os.MkdirAll(seed, 0700)
				metadata := rec(field(name, rec(field("source", rec(field("source", "directory"), field("path", filepath.Join(seed, "untrusted-build-time-mount")))), field("installLocation", "/stale/build/image"), field("lastUpdated", "2026-10-01T00:00:00.000Z"), field("futureSeed", true))))
				_ = os.WriteFile(filepath.Join(seed, "known_marketplaces.json"), raw(metadata), 0600)
				if profile == "missing-first-seed-falls-through" && index == 0 {
					continue
				}
				market := filepath.Join(seed, "marketplaces", name)
				_ = os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700)
				_ = os.WriteFile(filepath.Join(market, ".claude-plugin", "marketplace.json"), raw(rec(field("name", name), field("owner", rec(field("name", fmt.Sprintf("Seed%d", index)))), field("plugins", []any{}))), 0600)
			}
			manager.options.Seeds = seeds
			declared := rec()
			settings := rec()
			if profile == "official-fallback-preserves-seed" {
				settings = rec(field("enabledPlugins", rec(field("missing@claude-plugins-official", true))))
				declared = rec(field(name, rec(field("source", rec(field("source", "github"), field("repo", "anthropics/claude-plugins-official"))), field("sourceIsFallback", true))))
			}
			_ = os.WriteFile(filepath.Join(f.config, "settings.json"), raw(settings), 0600)
			zip := profile == "zip-steady-state"
			if zip {
				_, err := manager.RegisterSeeds(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := manager.Known(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			expected := sourcePluginReconciliation(t, f.home, map[string]any{"operation": "headless", "seedDirs": seeds, "zipCache": zip})
			_ = os.WriteFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"), raw(before), 0600)
			changed, err := manager.RegisterSeeds(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := manager.Reconcile(context.Background(), declared, f.workspace, ReconcileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			changed = changed || len(result.Installed)+len(result.Updated) > 0
			if string(raw(changed)) != string(expected.Get("result")) {
				t.Fatal("seed changed result", changed, string(expected.Get("result")))
			}
			actual, err := manager.Known(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !equalJSON(actual, obj(expected.Get("known"))) {
				t.Fatal("actual seed mapping", string(raw(actual)), string(expected.Get("known")))
			}
			if zip {
				// Remove the Source-produced ZIP metadata before the Go producer so
				// this proves a genuine steady-state sync, not a retained fixture.
				_ = os.Remove(filepath.Join(f.config, "plugins", "marketplaces", name+".json"))
				archive, err := pluginzip.New(pluginzip.Options{Home: f.home, TempDir: filepath.Join(f.home, "tmp"), Environment: map[string]string{"CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE": "1", "CLAUDE_CODE_PLUGIN_CACHE_DIR": filepath.Join(f.config, "plugins")}})
				if err != nil {
					t.Fatal(err)
				}
				if err = manager.SyncZipMarketplaces(context.Background(), archive); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(f.config, "plugins", "marketplaces", name+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if !equalJSON(obj(data), obj(obj(expected.Get("zipMarketplaces")).Get(name+".json"))) {
					t.Fatal("steady-state ZIP manifest differs", string(data), string(expected.Get("zipMarketplaces")))
				}
			}
		})
	}
}

func TestMarketplaceCanonicalRootsActualSourcePhysicalRetargetAndTwoLRULifetimes(t *testing.T) {
	f, manager := marketplaceReconciliationFixture(t)
	tree := filepath.Join(f.home, "tree")
	_ = os.MkdirAll(tree, 0700)
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := []string{filepath.Join(f.home, "first"), filepath.Join(f.home, "second")}
	links := []string{}
	for _, root := range roots {
		gitdir := filepath.Join(root, ".git", "worktrees", "tree")
		links = append(links, "gitdir: "+gitdir+"\n")
		write(filepath.Join(gitdir, "commondir"), "../..\n")
		write(filepath.Join(gitdir, "gitdir"), filepath.Join(tree, ".git")+"\n")
		write(filepath.Join(root, "market", ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[]}`)
	}
	write(filepath.Join(tree, ".git"), links[0])
	declared := rec(field("mkt", rec(field("source", rec(field("source", "directory"), field("path", "./market"))))))
	known := rec(field("mkt", rec(field("source", rec(field("source", "directory"), field("path", filepath.Join(roots[0], "market")))), field("installLocation", filepath.Join(roots[0], "market")), field("lastUpdated", "2026-10-01T00:00:00.000Z"))))
	evict := []string{}
	for index := 0; index < 50; index++ {
		path := filepath.Join(f.home, fmt.Sprintf("evict-%d", index))
		if err := os.MkdirAll(filepath.Join(path, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		evict = append(evict, path)
	}
	settings := rec(field("extraKnownMarketplaces", declared))
	write(filepath.Join(f.config, "settings.json"), string(raw(settings)))
	expected := sourcePluginReconciliation(t, f.home, map[string]any{"operation": "canonical-lifetime", "cwd": tree, "declared": declared, "known": known, "gitFile": filepath.Join(tree, ".git"), "gitReplacement": links[1], "evictCanonicalRoots": evict})
	var observations []protocol.JSONObject
	_ = json.Unmarshal(expected.Get("observations"), &observations)
	if len(observations) != 3 || string(observations[0].Get("upToDate")) != `["mkt"]` || string(observations[1].Get("upToDate")) != `["mkt"]` || string(observations[2].Get("upToDate")) != `[]` {
		t.Fatal("actual Source did not retain then evict its canonical root", string(expected.Get("observations")))
	}
	write(filepath.Join(tree, ".git"), links[0])
	write(filepath.Join(f.config, "plugins", "known_marketplaces.json"), string(raw(known)))
	for index := range observations {
		if index == 1 {
			write(filepath.Join(tree, ".git"), links[1])
		}
		if index == 2 {
			for _, path := range evict {
				manager.options.Roots.Canonical(path)
			}
		}
		actual, err := manager.Reconcile(context.Background(), declared, tree, ReconcileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if string(raw(actual.UpToDate)) != string(observations[index].Get("upToDate")) {
			t.Fatalf("actual reconcile step%d changed Source root lifetime: Go=%s Source=%s", index, raw(actual), observations[index].Get("upToDate"))
		}
	}
}
