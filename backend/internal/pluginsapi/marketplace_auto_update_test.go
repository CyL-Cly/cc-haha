package pluginsapi

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestMarketplaceAutoUpdateActualSourceDeclaringScopeAndPhysicalUnknownFields(t *testing.T) {
	seedSettings := func(scope string) string {
		return string(raw(map[string]any{"futureRoot": map[string]any{"keep": scope}, "extraKnownMarketplaces": map[string]any{"mkt": map[string]any{"source": map[string]any{"source": "directory", "path": "$HOME/market"}, "autoUpdate": false}, "untouched": map[string]any{"source": map[string]any{"source": "directory", "path": "$HOME/other"}, "autoUpdate": false}}}))
	}
	cases := []map[string]any{}
	for _, scope := range []string{"local", "project", "user", "absent"} {
		files := []map[string]any{{"path": "$CONFIG/plugins/known_marketplaces.json", "content": `{"mkt":{"source":{"source":"directory","path":"$HOME/market"},"installLocation":"$HOME/market","lastUpdated":"2026-10-01T00:00:00.000Z","futureEntry":{"keep":true},"autoUpdate":false}}`}}
		if scope != "absent" {
			files = append(files, map[string]any{"path": "$CONFIG/settings.json", "content": seedSettings("user")})
		}
		if scope == "project" || scope == "local" {
			files = append(files, map[string]any{"path": "$WORKSPACE/.claude/settings.json", "content": seedSettings("project")})
		}
		if scope == "local" {
			files = append(files, map[string]any{"path": "$WORKSPACE/.claude/settings.local.json", "content": seedSettings("local")})
		}
		cases = append(cases, map[string]any{"action": "marketplace-auto-update", "name": "mkt", "enabled": true, "reset": true, "clear": true, "files": files})
	}
	cases = append(cases, map[string]any{"action": "marketplace-auto-update", "name": "missing", "enabled": true, "reset": true, "clear": true, "files": []map[string]any{{"path": "$CONFIG/plugins/known_marketplaces.json", "content": `{"mkt":{"source":{"source":"directory","path":"$HOME/market"},"installLocation":"$HOME/market","lastUpdated":"2026-10-01T00:00:00.000Z","autoUpdate":false}}`}}})
	expected := sourcePlugins(t, cases)
	for i, row := range cases {
		t.Run([]string{"local", "project", "user", "absent", "missing"}[i], func(t *testing.T) {
			f := newFixture(t)
			f.seed(t, row)
			resolver, err := NewMarketplaceResolver(MarketplaceOptions{Home: f.home, WorkDir: f.workspace, PluginsDir: filepath.Join(f.config, "plugins"), Environment: map[string]string{"HOME": f.home}, Store: storage.New(storage.Options{}), Now: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			manager, err := NewMarketplaceManager(MarketplaceManagerOptions{Resolver: resolver, ReadSettings: func(ctx context.Context, scope, cwd string) (protocol.JSONObject, error) {
				source := map[string]string{"local": "localSettings", "project": "projectSettings", "user": "userSettings"}[scope]
				value, err := f.settings.GetEditableSource(source, f.workspace)
				if value == nil {
					return rec(), err
				}
				return value.Clone(), err
			}, WriteSettings: func(ctx context.Context, scope string, patch protocol.JSONObject, undefined [][]string) error {
				writes++
				source := map[string]string{"local": "localSettings", "project": "projectSettings", "user": "userSettings"}[scope]
				result, err := f.settings.UpdateEditableSource(source, f.workspace, clisettings.Patch{Object: patch})
				if err != nil {
					return err
				}
				return result.Error
			}, ClearCaches: func(context.Context) error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			err = manager.SetAutoUpdate(context.Background(), row["name"].(string), true)
			got := map[string]any{"files": normalize(f.snapshot(t), f.home)}
			if err != nil {
				got["error"] = err.Error()
			}
			if !reflect.DeepEqual(got, expected[i]) {
				t.Fatalf("Go=%s Source=%s", raw(got), raw(expected[i]))
			}
			if i < 3 && writes != 1 || i >= 3 && writes != 0 {
				t.Fatal("declaring source write count", writes)
			}
			if err == nil {
				before, _ := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
				previous := writes
				if err = manager.SetAutoUpdate(context.Background(), "mkt", true); err != nil {
					t.Fatal(err)
				}
				after, _ := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
				if string(before) != string(after) || writes != previous {
					t.Fatal("unchanged setting rewrote physical state")
				}
			}
		})
	}
}
