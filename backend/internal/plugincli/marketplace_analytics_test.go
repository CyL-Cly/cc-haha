package plugincli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestMarketplaceAnalyticsUsesActualSuccessfulOperationSourceAndCount(t *testing.T) {
	root := t.TempDir()
	plugins := filepath.Join(root, "plugins")
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"future":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := pluginsapi.NewMarketplaceResolver(pluginsapi.MarketplaceOptions{Home: root, WorkDir: root, PluginsDir: plugins, Environment: map[string]string{}, Store: storage.New(storage.Options{}), Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := pluginsapi.NewMarketplaceManager(pluginsapi.MarketplaceManagerOptions{Resolver: resolver,
		ReadSettings: func(_ context.Context, scope, _ string) (protocol.JSONObject, error) {
			if scope != "user" {
				return protocol.NewJSONObject(), nil
			}
			body, err := os.ReadFile(settingsPath)
			var value protocol.JSONObject
			if err == nil {
				err = json.Unmarshal(body, &value)
			}
			return value, err
		},
		WriteSettings: func(_ context.Context, scope string, value protocol.JSONObject, undefined [][]string) error {
			if scope != "user" {
				t.Error("unexpected actual settings producer", scope, undefined)
			}
			body, err := os.ReadFile(settingsPath)
			if err != nil {
				return err
			}
			var current protocol.JSONObject
			if err := json.Unmarshal(body, &current); err != nil {
				return err
			}
			current.Assign(value)
			for _, path := range undefined {
				if len(path) != 2 || path[0] != "extraKnownMarketplaces" {
					t.Error("unexpected actual delete path", path)
					continue
				}
				var object protocol.JSONObject
				if err := json.Unmarshal(current.Get(path[0]), &object); err != nil {
					return err
				}
				object.Delete(path[1])
				raw, err := object.MarshalJSON()
				if err != nil {
					return err
				}
				current.Set(path[0], raw)
			}
			body, err = current.MarshalJSON()
			if err != nil {
				return err
			}
			return os.WriteFile(settingsPath, body, 0600)
		}, ClearCaches: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	report := func(event analytics.Event) {
		body, err := event.Metadata.MarshalJSON()
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, event.Name+":"+string(body))
	}
	run := func(command Command) Result {
		t.Helper()
		result, err := RunMarketplaceWithAnalytics(context.Background(), command, manager, root, root, "darwin", report)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, name := range []string{"alpha", "beta"} {
		market := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(market, ".claude-plugin"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(market, ".claude-plugin", "marketplace.json"), []byte(`{"name":"`+name+`","owner":{"name":"Owned"},"plugins":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		if result := run(Command{Action: "marketplace-add", Argument: market}); result.ExitCode != 0 || !strings.Contains(result.Stdout, name) {
			t.Fatal("actual add failed", result)
		}
	}
	if result := run(Command{Action: "marketplace-update", Argument: "alpha"}); result.ExitCode != 0 {
		t.Fatal(result)
	}
	if result := run(Command{Action: "marketplace-update"}); result.ExitCode != 0 || !strings.Contains(result.Stdout, "updated 2") {
		t.Fatal(result)
	}
	if result := run(Command{Action: "marketplace-remove", Argument: "alpha"}); result.ExitCode != 0 {
		t.Fatal(result)
	}
	before := append([]string(nil), seen...)
	if result := run(Command{Action: "marketplace-update", Argument: "missing"}); result.ExitCode != 1 {
		t.Fatal("unknown marketplace succeeded", result)
	}
	if result := run(Command{Action: "marketplace-add", Argument: "not-a-source"}); result.ExitCode != 1 {
		t.Fatal("invalid source succeeded", result)
	}
	if !reflect.DeepEqual(before, seen) {
		t.Fatal("failed operation emitted success analytics", seen)
	}
	if expected := []string{`tengu_marketplace_added:{"source_type":"directory"}`, `tengu_marketplace_added:{"source_type":"directory"}`, `tengu_marketplace_updated:{"marketplace_name":"alpha"}`, `tengu_marketplace_updated_all:{"count":2}`, `tengu_marketplace_removed:{"marketplace_name":"alpha"}`}; !reflect.DeepEqual(seen, expected) {
		t.Fatal("actual operation source/count metadata changed", seen)
	}
	original, err := RunMarketplace(context.Background(), Command{Action: "marketplace-list", JSON: true}, manager, root, root, "darwin")
	if err != nil || original.ExitCode != 0 {
		t.Fatal(err, original)
	}
	reported := run(Command{Action: "marketplace-list", JSON: true})
	if !reflect.DeepEqual(original, reported) || !reflect.DeepEqual(before, seen) {
		t.Fatal("default API/list changed or acquired an invented list event", original, reported, seen)
	}
	if result := run(Command{Action: "marketplace-remove", Argument: "beta"}); result.ExitCode != 0 {
		t.Fatal(result)
	}
	count := len(seen)
	if result := run(Command{Action: "marketplace-update"}); result.ExitCode != 0 || !strings.Contains(result.Stdout, "No marketplaces configured") || len(seen) != count {
		t.Fatal("empty update acquired success analytics", result, seen)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(settings), `"future":{"keep":true}`) {
		t.Fatal("actual marketplace events damaged unrelated settings", err, string(settings))
	}
}
