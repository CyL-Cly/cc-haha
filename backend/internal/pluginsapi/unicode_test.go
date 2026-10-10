package pluginsapi

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"reflect"
	"strings"
	"testing"
)

func TestPluginDescriptionsPreserveActualSourceUTF16Truncation(t *testing.T) {
	for _, content := range []string{strings.Repeat("a", 96) + "😀 more text to exceed one hundred code units", "#　宽标题", "# Heading\u2028separate"} {
		files := seedPlugin("sample@mkt", "user", "", "1.0.0")
		files = append(files, map[string]any{"path": "$HOME/market/plugins/sample/commands/actual.md", "content": content})
		row := map[string]any{"method": "GET", "path": "/api/plugins/detail?id=sample%40mkt", "reset": true, "clear": true, "files": files, "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": true}}, "rawResponse": true}
		expected := sourcePlugins(t, []map[string]any{row})[0]
		source := obj([]byte(expected["bodyRaw"].(string)))
		detail := obj(source.Get("detail"))
		entries := arr(detail.Get("commandEntries"))
		if len(entries) != 1 {
			t.Fatal("Source raw description fixture did not load command")
		}
		want := obj(entries[0]).Get("description")
		f := newFixture(t)
		f.seed(t, row)
		actual, err := f.service.Detail(context.Background(), "sample@mkt", "")
		if err != nil {
			t.Fatal(err)
		}
		values := arr(actual.Get("commandEntries"))
		if len(values) != 1 {
			t.Fatal("native did not load command")
		}
		got := obj(values[0]).Get("description")
		actualUnits, actualErr := protocol.DecodeJavaScriptJSONString(got)
		sourceUnits, sourceErr := protocol.DecodeJavaScriptJSONString(want)
		if actualErr != nil || sourceErr != nil || !reflect.DeepEqual(actualUnits, sourceUnits) {
			t.Fatalf("actual Source description JSON mismatch\nGo %s\nSource %s", got, want)
		}
	}
}
