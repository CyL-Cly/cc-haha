package pluginsapi

import (
	"context"
	"encoding/json"
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

func TestPluginPolicyScopesMissingAndMalformedStateAgainstActualSource(t *testing.T) {
	cases := []map[string]any{{"method": "GET", "path": "/api/plugins", "reset": true, "clear": true}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "missing"}}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "missing", "scope": "user"}}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "missing@mkt"}}, {"method": "GET", "path": "/api/plugins"}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "missing@mkt"}}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "missing@mkt", "scope": "user"}}, {"method": "POST", "path": "/api/plugins/update", "body": map[string]any{"id": "missing@mkt", "scope": "managed"}}, {"method": "POST", "path": "/api/plugins/enable", "reset": true, "clear": true, "settings": map[string]any{"enabledPlugins": map[string]any{"missing@mkt": false}}, "files": []map[string]any{{"path": "$HOME/managed/managed-settings.json", "content": `{"enabledPlugins":{"missing@mkt":false}}`}}, "body": map[string]any{"id": "missing"}}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "missing@mkt", "scope": "project", "keepData": nil}}, {"method": "GET", "path": "/api/plugins", "reset": true, "clear": true, "files": []map[string]any{{"path": "$CONFIG/plugins/known_marketplaces.json", "content": "{"}}}, {"method": "GET", "path": "/api/plugins/detail?id=missing%40mkt"}, {"method": "POST", "path": "/api/plugins/unknown", "raw": "\n {} "}}
	files := seedPlugin("sample@mkt", "project", "$WORKSPACE", "1.0.0")
	cases = append(cases, map[string]any{"method": "GET", "path": "/api/plugins?cwd=$WORKSPACE/subdir", "reset": true, "clear": true, "files": append(files, map[string]any{"path": "$WORKSPACE/subdir/keep", "content": "currentcwd"}), "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": true}}}, map[string]any{"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "user"}}, map[string]any{"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "project", "cwd": "$WORKSPACE"}})
	compareCases(t, cases)
}
func TestPluginLastScopeSecretsAndKeepDataAgainstActualSource(t *testing.T) {
	files := seedPlugin("sample@mkt", "user", "", "1.0.0")
	for _, file := range files {
		if strings.HasSuffix(file["path"].(string), "installed_plugins.json") {
			var data map[string]any
			_ = json.Unmarshal([]byte(file["content"].(string)), &data)
			plugins := data["plugins"].(map[string]any)
			entries := plugins["sample@mkt"].([]any)
			local := map[string]any{}
			for key, value := range entries[0].(map[string]any) {
				local[key] = value
			}
			local["scope"] = "project"
			local["projectPath"] = "$WORKSPACE"
			plugins["sample@mkt"] = append(entries, local)
			file["content"] = string(raw(data))
		}
	}
	files = append(files, map[string]any{"path": "$CONFIG/.credentials.json", "content": `{"futureSecure":true,"pluginSecrets":{"sample@mkt":{"fakeSecret":"owned-test-only"},"sample@mkt/server":{"fakeSecret":"owned-server-only"},"other@mkt":{"keep":"other-private-fixture"}}}`}, map[string]any{"path": "$CONFIG/plugins/data/sample-mkt/data", "content": "should survive first scope only"})
	cases := []map[string]any{{"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "user"}, "reset": true, "clear": true, "files": files, "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": true}, "pluginConfigs": map[string]any{"sample@mkt": map[string]any{"options": map[string]any{"flag": true}}, "other@mkt": map[string]any{"options": map[string]any{"keep": true}}}}}, {"method": "GET", "path": "/api/plugins/detail?id=sample%40mkt&cwd=$WORKSPACE"}, {"method": "POST", "path": "/api/plugins/uninstall", "body": map[string]any{"id": "sample@mkt", "scope": "project", "cwd": "$WORKSPACE"}}}
	compareCases(t, cases)
}
func TestPluginMarketplaceMemoLifetimeAgainstActualSource(t *testing.T) {
	var hits atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"mkt","owner":{"name":"owner"},"plugins":[{"name":"sample","source":"./sample"}]}`))
	}))
	defer host.Close()
	known := map[string]any{"mkt": map[string]any{"source": map[string]any{"source": "url", "url": host.URL}, "installLocation": "$CONFIG/plugins/marketplaces/mkt", "lastUpdated": "2026-10-01T00:00:00.000Z"}}
	row := map[string]any{"action": "lookup", "id": "sample@mkt", "reset": true, "clear": true, "files": []map[string]any{{"path": "$CONFIG/plugins/known_marketplaces.json", "content": string(raw(known))}}}
	cases := []map[string]any{row, row}
	expected := sourcePlugins(t, cases)
	f := newFixture(t)
	resolver, err := NewMarketplaceResolver(MarketplaceOptions{Home: f.home, WorkDir: f.workspace, PluginsDir: filepath.Join(f.config, "plugins"), Environment: map[string]string{}, Store: storage.New(storage.Options{}), Now: func() time.Time { return time.UnixMilli(1791244800000) }})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		f.seed(t, c)
		plugin, err := resolver.Lookup(context.Background(), "sample@mkt")
		if err != nil || plugin == nil {
			t.Fatal(plugin, err)
		}
		got := map[string]any{"plugin": normalize(map[string]any{"entry": plugin.Entry, "marketplaceInstallLocation": plugin.InstallLocation}, f.home), "files": normalize(f.snapshot(t), f.home)}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("memo case %d Go=%s Source=%s", i, raw(got), raw(expected[i]))
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("Source/native memo performed repeated network work: %d", hits.Load())
	}
	if _, err := os.Stat(filepath.Join(f.config, "plugins", "marketplaces", "mkt")); !os.IsNotExist(err) {
		t.Fatal("memo incorrectly restored removed cache after original clear", err)
	}
}
func TestPluginBuiltinOperationsKeepSourceSuffixContract(t *testing.T) {
	cases := []map[string]any{{"method": "POST", "path": "/api/plugins/enable", "reset": true, "clear": true, "body": map[string]any{"id": "undeclared@builtin"}}, {"method": "POST", "path": "/api/plugins/enable", "body": map[string]any{"id": "undeclared@builtin"}}, {"method": "POST", "path": "/api/plugins/disable", "body": map[string]any{"id": "undeclared@builtin", "scope": "local", "cwd": "$WORKSPACE"}}, {"method": "GET", "path": "/api/plugins"}, {"method": "POST", "path": "/api/plugins/enable", "raw": `{"id":"ordinary@mkt","":0,"other":true}`}}
	compareCases(t, cases)
}
