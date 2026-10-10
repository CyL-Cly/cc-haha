package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInsightsAggregateRealStatsFacetArraysOrderingMediansAndOldSchemaMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	base, err := LogMeta(Log{SessionID: "session-one", CreatedMillis: 1767225600000, ModifiedMillis: 1767225720000, FirstPrompt: strings.Repeat("😀", 100), Messages: []protocol.JSONObject{}}, StatsOptions{ParseDate: parseISO, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	base.Set("user_message_count", raw(3))
	base.Set("user_response_times", raw([]float64{10, 2, 6, 4}))
	base.Set("tool_counts", raw(map[string]int{"Read": 2, "Write": 1}))
	base.Set("project_path", raw("/private/project"))
	base.Set("user_message_timestamps", raw([]string{"2026-01-01T00:00:00Z", "2026-01-01T00:02:00Z"}))
	second := base.Clone()
	second.Set("session_id", raw("session-two"))
	second.Set("start_time", raw("2026-01-02T00:00:00Z"))
	second.Set("user_message_count", raw(2))
	second.Set("user_response_times", raw([]float64{30}))
	second.Set("user_message_timestamps", raw([]string{"2026-01-01T00:01:00Z"}))
	second.Set("uses_mcp", raw(true))
	var facet protocol.JSONObject
	_ = facet.UnmarshalJSON([]byte(`{"underlying_goal":"finish","outcome":"fully_achieved","brief_summary":"done","goal_categories":{"fix_bug":2,"warmup_minimal":0,"negative":-1},"user_satisfaction_counts":{"happy":1},"claude_helpfulness":"essential","session_type":"iterative_refinement","friction_counts":[0,2],"primary_success":"none"}`))
	malformed := base.Clone()
	malformed.Delete("tool_error_categories")
	stringCounts := base.Clone()
	for _, name := range []string{"user_message_count", "input_tokens", "output_tokens", "git_commits", "git_pushes", "user_interruptions", "tool_errors", "lines_added", "lines_removed", "files_modified"} {
		stringCounts.Set(name, raw("2"))
	}
	missingCounts := base.Clone()
	missingCounts.Delete("input_tokens")
	type scenario struct {
		Sessions []protocol.JSONObject          `json:"sessions"`
		Facets   map[string]protocol.JSONObject `json:"facets"`
	}
	rows := []scenario{{[]protocol.JSONObject{}, map[string]protocol.JSONObject{}}, {[]protocol.JSONObject{base}, map[string]protocol.JSONObject{}}, {[]protocol.JSONObject{base, second}, map[string]protocol.JSONObject{"session-one": facet}}, {[]protocol.JSONObject{malformed}, map[string]protocol.JSONObject{}}, {[]protocol.JSONObject{stringCounts, second}, map[string]protocol.JSONObject{}}, {[]protocol.JSONObject{missingCounts, second}, map[string]protocol.JSONObject{}}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/aggregation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_AGGREGATION_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Rows []struct {
			Value map[string]any
			Error any
		}
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		got, err := Aggregate(row.Sessions, row.Facets, parseISO)
		if err != nil {
			if source.Rows[i].Error == nil {
				t.Error(i, err)
			}
			continue
		}
		var actual map[string]any
		_ = json.Unmarshal(raw(got), &actual)
		if !reflect.DeepEqual(actual, source.Rows[i].Value) {
			t.Errorf("case%d Go%s Source%s", i, raw(actual), raw(source.Rows[i].Value))
		}
	}
}
