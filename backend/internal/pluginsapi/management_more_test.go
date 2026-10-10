package pluginsapi

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPluginMarketplaceFallbackAgainstActualSource(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("User-Agent") != "Claude-Code-Plugin-Manager" || r.Header.Get("X-Fixture") != "private-header" {
			http.Error(w, "wrong headers", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"mkt","owner":{"name":"owner"},"plugins":[{"name":"sample","source":"./sample"}]}`))
	}))
	defer server.Close()
	caseFor := func(source map[string]any, location string, files []map[string]any) map[string]any {
		known := map[string]any{"mkt": map[string]any{"source": source, "installLocation": location, "lastUpdated": "2026-10-01T00:00:00.000Z"}}
		files = append(files, map[string]any{"path": "$CONFIG/plugins/known_marketplaces.json", "content": string(raw(known))})
		return map[string]any{"action": "lookup", "id": "sample@mkt", "reset": true, "clear": true, "files": files}
	}
	cases := []map[string]any{caseFor(map[string]any{"source": "url", "url": server.URL, "headers": map[string]any{"X-Fixture": "private-header", "User-Agent": "must-override"}}, "$CONFIG/plugins/marketplaces/mkt", nil), caseFor(map[string]any{"source": "settings", "name": "mkt", "plugins": []any{map[string]any{"name": "sample", "source": map[string]any{"source": "url", "url": "https://example.invalid/plugin.git"}}}}, "$CONFIG/plugins/marketplaces/mkt", nil), caseFor(map[string]any{"source": "directory", "path": "$HOME/original"}, "$HOME/missing-cache", []map[string]any{{"path": "$HOME/original/.claude-plugin/marketplace.json", "content": `{"name":"mkt","owner":{"name":"owner"},"plugins":[{"name":"sample","source":"./sample"}]}`}}), caseFor(map[string]any{"source": "file", "path": "$HOME/from-file/.claude-plugin/marketplace.json"}, "$HOME/missing-cache", []map[string]any{{"path": "$HOME/from-file/.claude-plugin/marketplace.json", "content": `{"name":"mkt","owner":{"name":"owner"},"plugins":[{"name":"sample","source":"./sample"}]}`}}), caseFor(map[string]any{"source": "git", "url": "file://$HOME/repo", "sparsePaths": []string{".claude-plugin", "plugins"}}, "$CONFIG/plugins/marketplaces/mkt", []map[string]any{{"path": "$HOME/repo/.claude-plugin/marketplace.json", "content": `{"name":"mkt","owner":{"name":"owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`}, {"path": "$HOME/repo/plugins/sample/README.md", "content": "private real clone"}, {"path": "$HOME/repo/elsewhere/excluded.md", "content": "sparse should exclude"}})}
	cases[len(cases)-1]["git"] = "$HOME/repo"
	for i, row := range cases {
		expected := sourcePlugins(t, []map[string]any{row})[0]
		f := newFixture(t)
		resolver, err := NewMarketplaceResolver(MarketplaceOptions{Home: f.home, WorkDir: f.workspace, PluginsDir: filepath.Join(f.config, "plugins"), Environment: map[string]string{"PATH": os.Getenv("PATH"), "HOME": f.home, "GIT_CONFIG_NOSYSTEM": "1"}, Store: storage.New(storage.Options{}), Now: func() time.Time { return time.UnixMilli(1791244800000) }})
		if err != nil {
			t.Fatal(err)
		}
		f.seed(t, row)
		plugin, err := resolver.Lookup(context.Background(), row["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if plugin == nil || expected["plugin"] == nil {
			known, knownErr := ReadKnownMarketplaces(context.Background(), filepath.Join(f.config, "plugins"))
			var fetchErr error
			if knownErr == nil {
				_, fetchErr = resolver.Fetch(context.Background(), obj(obj(known.Get("mkt")).Get("source")))
			}
			t.Fatalf("supported fallback case %d did not actually load its marketplace: Go=%v Source=%v known=%v fetch=%v", i, plugin, expected["plugin"], knownErr, fetchErr)
		}
		var value any
		if plugin != nil {
			value = map[string]any{"entry": plugin.Entry, "marketplaceInstallLocation": plugin.InstallLocation}
		}
		got := map[string]any{"plugin": normalize(value, f.home), "files": normalize(f.snapshot(t), f.home)}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("Fallback case %d\nGo=%s\nSource=%s", i, raw(got), raw(expected))
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("Expected Source+native real loopback fetch, got %d", requests.Load())
	}
}
func TestPluginHookRegistrationPruneAgainstActualSource(t *testing.T) {
	files := seedPlugin("sample@mkt", "user", "", "1.0.0")
	files = append(files, map[string]any{"path": "$CONFIG/plugins/cache/mkt/sample/1.0.0/hooks/hooks.json", "content": `{"hooks":{"Stop":[{"matcher":"one","hooks":[{"type":"command","command":"fixture-one"}]}],"PreToolUse":[{"matcher":"Read","hooks":[{"type":"prompt","prompt":"Fixture prompt"}]}]}}`})
	files = seedLocalComponents(files)
	cases := []map[string]any{{"action": "hooks", "operation": "load", "reset": true, "clear": true, "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": true}}, "files": files, "callbacks": map[string]any{"Stop": []any{map[string]any{"matcher": "callback", "hooks": []any{}}}}}, {"action": "hooks", "operation": "clear"}, {"action": "hooks", "operation": "prune", "clear": true, "files": []map[string]any{{"path": "$CONFIG/settings.json", "content": `{"enabledPlugins":{"sample@mkt":false}}`}}}, {"action": "hooks", "operation": "load", "clear": true}}
	expected := sourcePlugins(t, cases)
	f := newFixture(t)
	registry := pluginhooks.New()
	for i, row := range cases {
		f.seed(t, row)
		if row["clear"] == true {
			registry.Clear()
		}
		if callbacks, ok := row["callbacks"]; ok {
			registry.Register(obj(raw(callbacks)))
		}
		if row["operation"] == "clear" {
			registry.Clear()
		} else {
			state, err := f.loader.LoadCached(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if row["operation"] == "load" {
				registry.LoadPlugins(state.Enabled)
			} else {
				registry.Prune(state.Enabled)
			}
		}
		got := normalize(map[string]any{"hooks": registry.Snapshot()}, f.home)
		if i == 0 && (!strings.Contains(string(raw(got)), "fixture-one") || !strings.Contains(string(raw(expected[i])), "fixture-one")) {
			state, _ := f.loader.LoadCached(context.Background())
			t.Fatalf("Hook fixture did not actually register its configured plugin hooks: state=%s Source=%s Go=%s", raw(state), raw(expected[i]), raw(got))
		}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("Hooks case %d\nGo=%s\nSource=%s", i, raw(got), raw(expected[i]))
		}
	}
}
func TestPluginUnknownPersistedFieldsSurviveNativeUpdate(t *testing.T) {
	f := newFixture(t)
	files := seedPlugin("sample@mkt", "user", "", "1.0.0")
	for _, file := range files {
		if strings.HasSuffix(file["path"].(string), "installed_plugins.json") {
			current := obj([]byte(file["content"].(string)))
			current.Set("futureRoot", raw("keep"))
			plugins := obj(current.Get("plugins"))
			entries := arr(plugins.Get("sample@mkt"))
			entry := obj(entries[0])
			entry.Set("futureEntry", raw(protocol.NewJSONObject(field("keep", true))))
			plugins.Set("sample@mkt", raw([]protocol.JSONObject{entry}))
			current.Set("plugins", raw(plugins))
			file["content"] = string(raw(current))
		}
	}
	f.seed(t, map[string]any{"reset": true, "clear": true, "files": files})
	_ = os.WriteFile(filepath.Join(f.home, "market", "plugins", "sample", ".claude-plugin", "plugin.json"), []byte(`{"name":"sample","version":"2.0.0"}`), 0600)
	if _, err := f.service.Update(context.Background(), "sample@mkt", "user", ""); err != nil {
		t.Fatal(err)
	}
	disk, err := f.service.options.Installed.Disk()
	if err != nil {
		t.Fatal(err)
	}
	if disk.Get("futureRoot") != nil {
		t.Fatal("validated projection unexpectedly exposed extension")
	}
	bytes, err := os.ReadFile(filepath.Join(f.config, "plugins", "installed_plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	actual := obj(bytes)
	if actual.String("futureRoot") != "keep" || obj(arr(obj(actual.Get("plugins")).Get("sample@mkt"))[0]).Get("futureEntry") == nil {
		t.Fatal("Owned update discarded forward-compatible fields")
	}
}
