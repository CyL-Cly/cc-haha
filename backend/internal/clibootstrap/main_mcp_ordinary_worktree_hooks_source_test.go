package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeMCPOrdinaryWorktreeHooksPhysicalInputAndRemovalActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	tree := filepath.Join(home, "hook-tree")
	createInput, removeInput := filepath.Join(home, "create-input.json"), filepath.Join(home, "remove-input.json")
	settings := map[string]any{"hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/bin/cat > " + createInput + "; /bin/mkdir -p " + tree + "; printf '" + tree + "'"}}}}, "WorktreeRemove": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/bin/cat > " + removeInput + "; /bin/rm -rf " + tree}}}}}, "future": map[string]any{"keep": true}}
	if e := os.WriteFile(filepath.Join(home, ".claude/settings.json"), rawMain(settings), 0600); e != nil {
		t.Fatal(e)
	}
	type row struct {
		name string
		args map[string]any
	}
	rows := []row{{"EnterWorktree", map[string]any{"name": "hook-owned"}}, {"ExitWorktree", map[string]any{"action": "remove"}}, {"ExitWorktree", map[string]any{"action": "remove", "discard_changes": true}}}
	source := startOrdinarySource(t, home, home, env)
	wants := make([]any, len(rows))
	for i, row := range rows {
		wants[i] = mcpOrdinaryResult(t, source.call(t, row.name, row.args))
	}
	source.close(t)
	read := func(path string) protocol.JSONObject {
		t.Helper()
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal("actual hook physical input missing", e)
		}
		var value protocol.JSONObject
		if e = value.UnmarshalJSON(data); e != nil {
			t.Fatal(e)
		}
		return value
	}
	sourceCreate, sourceRemove := read(createInput), read(removeInput)
	if _, e := os.Stat(tree); !os.IsNotExist(e) {
		t.Fatal("Source actual removal missing", e)
	}
	_ = os.Remove(createInput)
	_ = os.Remove(removeInput)
	host, _ := ordinaryGoContext(t, home, home, env)
	for i, row := range rows {
		got, e := host.Call(context.Background(), row.name, rawMain(row.args))
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(mcpOrdinaryResult(t, got), wants[i]) {
			t.Errorf("%d %s Go=%v Source=%v", i, row.name, mcpOrdinaryResult(t, got), wants[i])
		}
	}
	if e := host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for i, pair := range [][2]protocol.JSONObject{{read(createInput), sourceCreate}, {read(removeInput), sourceRemove}} {
		a, b := pair[0], pair[1]
		a.Delete("session_id")
		b.Delete("session_id")
		a.Delete("transcript_path")
		b.Delete("transcript_path")
		var x, y any
		_ = json.Unmarshal(rawMain(a), &x)
		_ = json.Unmarshal(rawMain(b), &y)
		if !reflect.DeepEqual(x, y) {
			t.Errorf("actual hook %d input Go=%v Source=%v", i, x, y)
		}
	}
	if _, e := os.Stat(tree); !os.IsNotExist(e) {
		t.Fatal("Go actual removal missing", e)
	}
}
