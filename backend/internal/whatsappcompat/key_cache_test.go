package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualOriginalSignalKeyCacheSharesValuesAndPreservesCachedNull(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_cache.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want any
	if e = json.Unmarshal(out, &want); e != nil {
		t.Fatal(e)
	}
	reads, writes := []any{}, []any{}
	data := map[string]map[string]any{"session": {"key": map[string]any{"value": 1}, "missing": nil, "false": false, "zero": 0, "empty": ""}}
	cache := NodeSignalKeyCache{Read: func(_ context.Context, category string, ids []string) (map[string]any, error) {
		reads = append(reads, map[string]any{"type": category, "ids": ids})
		values := map[string]any{}
		for _, id := range ids {
			values[id] = data[category][id]
		}
		return values, nil
	}, Write: func(_ context.Context, values map[string]map[string]any) error {
		writes = append(writes, values)
		for category, entries := range values {
			for id, v := range entries {
				data[category][id] = v
			}
		}
		return nil
	}}
	snapshots := []any{}
	capture := func(v any) {
		var immutable any
		_ = json.Unmarshal(nodeJSON(v), &immutable)
		snapshots = append(snapshots, immutable)
	}
	first, e := cache.Get(ctx, "session", []string{"key", "missing", "false", "zero", "empty"})
	if e != nil {
		t.Fatal(e)
	}
	capture(first)
	first["key"].(map[string]any)["value"] = 2
	v, e := cache.Get(ctx, "session", []string{"key", "missing"})
	if e != nil {
		t.Fatal(e)
	}
	capture(v)
	if e = cache.Set(ctx, map[string]map[string]any{"session": {"key": map[string]any{"value": 3}, "missing": nil}}); e != nil {
		t.Fatal(e)
	}
	v, e = cache.Get(ctx, "session", []string{"key", "missing"})
	if e != nil {
		t.Fatal(e)
	}
	capture(v)
	cache.Clear()
	v, e = cache.Get(ctx, "session", []string{"key", "missing"})
	if e != nil {
		t.Fatal(e)
	}
	capture(v)
	got := map[string]any{"snapshots": snapshots, "reads": reads, "writes": writes}
	if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
		t.Fatalf("native%s Source%s", nodeJSON(got), nodeJSON(want))
	}
}
