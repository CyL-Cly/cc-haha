package pluginsapi

import (
	"context"
	"reflect"
	"testing"
)

func TestPluginInteractiveUpdateActualSourceVersionsPhysicalCacheAndOrphans(t *testing.T) {
	cases := []map[string]any{}
	for _, scope := range []string{"user", "managed", "local", "project"} {
		project := ""
		if scope == "project" || scope == "local" {
			project = "$WORKSPACE"
		}
		cases = append(cases, map[string]any{"action": "update-op", "id": "sample@mkt", "scope": scope, "reset": true, "clear": true, "files": seedPlugin("sample@mkt", scope, project, "1.0.0"), "settings": map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": true}, "future": map[string]bool{"keep": true}}})
		cases = append(cases, map[string]any{"action": "update-op", "id": "sample@mkt", "scope": scope, "files": []map[string]any{{"path": "$HOME/market/plugins/sample/.claude-plugin/plugin.json", "content": `{"name":"sample","version":"2.0.0"}`}}})
		cases = append(cases, map[string]any{"action": "update-op", "id": "sample@mkt", "scope": scope})
	}
	source := sourcePlugins(t, cases)
	f := newFixture(t)
	for i, row := range cases {
		f.seed(t, row)
		value, err := f.service.UpdateInteractive(context.Background(), row["id"].(string), row["scope"].(string), "")
		if err != nil {
			t.Fatal(i, err)
		}
		actual := map[string]any{"body": normalize(value, f.home), "files": normalize(f.snapshot(t), f.home)}
		if !reflect.DeepEqual(actual, source[i]) {
			t.Fatalf("update case%d Go%s Source%s", i, raw(actual), raw(source[i]))
		}
	}
}
