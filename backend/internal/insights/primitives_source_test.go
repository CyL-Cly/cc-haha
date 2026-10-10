package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parseISO(s string) (float64, error) {
	value, valid, err := activitystatsapi.NewIndexDateParser(time.UTC).Parse(s)
	if err != nil {
		return 0, err
	}
	if !valid {
		return math.NaN(), nil
	}
	return value, nil
}

func TestInsightsOverlapBranchSelectionAndShallowCacheSchemaMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	session := func(id string, stamps ...string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: raw(id)}, protocol.JSONField{Name: "user_message_timestamps", Value: raw(stamps)})
	}
	sets := [][]protocol.JSONObject{{}, {session("one", "2026-01-01T00:00:00Z"), session("two", "2026-01-01T00:01:00Z")}, {session("one", "2026-01-01T00:00:00Z", "2026-01-01T00:02:00Z"), session("two", "2026-01-01T00:01:00Z")}, {session("one", "2026-01-01T00:00:00Z", "2026-01-01T00:30:00Z"), session("two", "2026-01-01T00:01:00Z")}, {session("one", "2026-01-01T00:00:00Z", "2026-01-01T00:30:01Z"), session("two", "2026-01-01T00:01:00Z")}, {session("colon:one", "2026-01-01T00:00:00Z", "2026-01-01T00:02:00Z"), session("other", "2026-01-01T00:01:00Z")}, {session("one", "2026-01-01T00:00:00Z", "2026-01-01T00:02:00Z", "2026-01-01T00:04:00Z"), session("two", "2026-01-01T00:01:00Z", "2026-01-01T00:03:00Z")}, {session("invalid", "broken date", "2026-01-01T00:02:00Z"), session("other", "2026-01-01T00:01:00Z")}}
	meta := func(id string, count, duration float64) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: raw(id)}, protocol.JSONField{Name: "user_message_count", Value: raw(count)}, protocol.JSONField{Name: "duration_minutes", Value: raw(duration)})
	}
	metas := [][]protocol.JSONObject{{}, {meta("one", 2, 3), meta("one", 3, 2), meta("one", 3, 4), meta("two", 2, 8), meta("one", 3, 4)}, {meta("__proto__", 2, 3), meta("__proto__", 1, 10), meta("constructor", 2, 1)}}
	valid := json.RawMessage(`{"underlying_goal":"x","outcome":"future","brief_summary":"done","goal_categories":[],"user_satisfaction_counts":{},"friction_counts":[],"future":1}`)
	facets := []json.RawMessage{valid, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{}`), json.RawMessage(`{"underlying_goal":1}`), json.RawMessage(`{"underlying_goal":"x","outcome":"future","brief_summary":"done","goal_categories":null,"user_satisfaction_counts":{},"friction_counts":[]}`)}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/primitives.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_PRIMITIVES_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "sessions": sets, "metas": metas, "facets": facets}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Multi []protocol.JSONObject
		Dedup [][]int
		Valid []bool
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, set := range sets {
		actual, err := MultiClauding(set, parseISO)
		if err != nil {
			t.Fatal(err)
		}
		a, b := raw(actual), raw(source.Multi[i])
		if !bytes.Equal(a, b) {
			t.Errorf("overlap%d Go%s Source%s", i, a, b)
		}
	}
	for i, set := range metas {
		actual := []int{}
		for _, selected := range Deduplicate(set) {
			for index, candidate := range set {
				if selected.Get("session_id") != nil && reflect.DeepEqual(selected, candidate) {
					actual = append(actual, index)
					break
				}
			}
		}
		if !reflect.DeepEqual(actual, source.Dedup[i]) {
			t.Errorf("dedup%d Go%v Source%v", i, actual, source.Dedup[i])
		}
	}
	for i, facet := range facets {
		if ValidFacets(facet) != source.Valid[i] {
			t.Errorf("facets%d Go%v Source%v", i, ValidFacets(facet), source.Valid[i])
		}
	}
}
