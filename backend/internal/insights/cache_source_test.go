package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type physicalCacheFiles struct{}

func (physicalCacheFiles) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (physicalCacheFiles) WriteFile(p string, b []byte, m fs.FileMode) error {
	return os.WriteFile(p, b, m)
}
func (physicalCacheFiles) MkdirAll(p string, m fs.FileMode) error { return os.MkdirAll(p, m) }
func (physicalCacheFiles) Remove(p string) error                  { return os.Remove(p) }

func TestInsightsCachePhysicalOriginalDataModesAndCorruptPruningMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	sourceHome := filepath.Join(home, "source")
	goHome := filepath.Join(home, "go")
	_ = os.MkdirAll(sourceHome, 0700)
	_ = os.MkdirAll(goHome, 0700)
	facets := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: raw("valid")}, protocol.JSONField{Name: "underlying_goal", Value: raw("real objective")}, protocol.JSONField{Name: "outcome", Value: raw("fully_achieved")}, protocol.JSONField{Name: "brief_summary", Value: raw("done")}, protocol.JSONField{Name: "goal_categories", Value: raw([]int{1, 2})}, protocol.JSONField{Name: "user_satisfaction_counts", Value: raw(map[string]int{})}, protocol.JSONField{Name: "friction_counts", Value: raw(map[string]int{})}, protocol.JSONField{Name: "future", Value: json.RawMessage(`"lone\ud800"`)})
	actions := []map[string]any{{"kind": "meta", "op": "load", "id": "missing"}, {"kind": "meta", "op": "load", "id": "old", "raw": `{"session_id":"old","old_field":true}`}, {"kind": "meta", "op": "load", "id": "primitive", "raw": "7"}, {"kind": "meta", "op": "load", "id": "null", "raw": "null"}, {"kind": "meta", "op": "load", "id": "broken", "raw": "{broken"}, {"kind": "facets", "op": "load", "id": "broken", "raw": "{broken"}, {"kind": "facets", "op": "load", "id": "corrupt", "raw": `{"session_id":"corrupt","underlying_goal":"old"}`}, {"kind": "facets", "op": "load", "id": "array", "raw": "[]"}, {"kind": "facets", "op": "save", "id": "valid", "value": facets}, {"kind": "facets", "op": "load", "id": "valid"}, {"kind": "meta", "op": "save", "id": "valid", "value": facets}}
	fixture, _ := filepath.Abs("testdata/cache.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_CACHE_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": sourceHome, "actions": actions}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct{ Rows []map[string]any }
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	cache, err := NewCache(filepath.Join(goHome, ".claude"), physicalCacheFiles{})
	if err != nil {
		t.Fatal(err)
	}
	for i, action := range actions {
		dir := cache.MetaDir()
		if action["kind"] == "facets" {
			dir = cache.FacetsDir()
		}
		path := filepath.Join(dir, action["id"].(string)+".json")
		if text, exists := action["raw"]; exists {
			_ = os.MkdirAll(dir, 0777)
			if err = os.WriteFile(path, []byte(text.(string)), 0666); err != nil {
				t.Fatal(err)
			}
		}
		var value any
		if action["op"] == "load" {
			var cached json.RawMessage
			if action["kind"] == "facets" {
				cached = cache.LoadFacets(action["id"].(string))
			} else {
				cached = cache.LoadMeta(action["id"].(string))
			}
			if len(cached) > 0 {
				_ = json.Unmarshal(cached, &value)
			}
		} else {
			if action["kind"] == "facets" {
				err = cache.SaveFacets(facets)
			} else {
				err = cache.SaveMeta(facets)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		var disk, mode any
		if contents, e := os.ReadFile(path); e == nil {
			disk = string(contents)
			info, _ := os.Stat(path)
			mode = float64(info.Mode().Perm())
		}
		actual := map[string]any{"value": value, "error": nil, "disk": disk, "mode": mode}
		if !reflect.DeepEqual(actual, source.Rows[i]) {
			t.Errorf("case%d Go%v Source%v", i, actual, source.Rows[i])
		}
	}
}
