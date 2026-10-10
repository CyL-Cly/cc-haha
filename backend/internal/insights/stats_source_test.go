package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInsightsActualTranscriptToolStatsLanguagesErrorsTimesAndEditsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	decode := func(text string) protocol.JSONObject {
		var record protocol.JSONObject
		if err := record.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return record
	}
	records := []protocol.JSONObject{
		decode(`{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"content":"human initial"}}`),
		decode(`{"type":"assistant","timestamp":"2026-01-01T00:01:00Z","message":{"usage":{"input_tokens":10,"output_tokens":3},"content":[{"type":"tool_use","name":"Write","input":{"file_path":"foo.ts","content":"a\nb\n"}},{"type":"tool_use","name":"Edit","input":{"file_path":"foo.ts","old_string":"a\nb\n","new_string":"a\nc\nd\n"}},{"type":"tool_use","name":"Bash","input":{"command":"git commit -m x && git push"}}]}}`),
		decode(`{"type":"user","timestamp":"2026-01-01T00:01:01Z","message":{"content":[{"type":"tool_result","content":"Exit code 7 rejected","is_error":true}]}}`),
		decode(`{"type":"user","timestamp":"2026-01-01T00:01:10Z","message":{"content":[{"type":"text","text":"[Request interrupted by user]"},{"type":"tool_result","is_error":true,"content":"file not found"}]}}`),
		decode(`{"type":"assistant","timestamp":"2026-01-01T01:00:00Z","message":{"usage":{"input_tokens":7,"output_tokens":2},"content":[{"type":"tool_use","name":"Task","input":{}},{"type":"tool_use","name":"Agent","input":{}},{"type":"tool_use","name":"mcp__actual__tool","input":{}},{"type":"tool_use","name":"WebSearch","input":{}},{"type":"tool_use","name":"WebFetch","input":{}},{"type":"tool_use","name":"Read","input":{"file_path":"script.PY"}},{"type":"tool_use","name":"Write","input":{"file_path":".go","content":""}}]}}`),
		decode(`{"type":"user","timestamp":"broken date","message":{"content":"invalid date remains observation"}}`),
	}
	logs := []Log{{SessionID: "actual-session", ProjectPath: "/private/project", FirstPrompt: "human initial", CreatedMillis: 1767225600000, ModifiedMillis: 1767225750000, Messages: records}, {SessionID: "empty", CreatedMillis: 1767225600000, ModifiedMillis: 1767225570000, Messages: []protocol.JSONObject{}}, {SessionID: "whitespace", CreatedMillis: 1767225600000, ModifiedMillis: 1767225600000, Messages: []protocol.JSONObject{decode(`{"type":"user","message":{"content":"\ufeff "}}`), decode(`{"type":"user","message":{"content":[{"type":"text","text":""}]}}`)}}, {SessionID: "prototype", CreatedMillis: 1767225600000, ModifiedMillis: 1767225600000, Messages: []protocol.JSONObject{decode(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"__proto__","input":{}},{"type":"tool_use","name":"constructor","input":{}}]}}`)}}}
	inputLogs := []map[string]any{}
	for _, log := range logs {
		row := map[string]any{"sessionId": log.SessionID, "projectPath": log.ProjectPath, "firstPrompt": log.FirstPrompt, "createdMillis": log.CreatedMillis, "modifiedMillis": log.ModifiedMillis, "messages": log.Messages}
		if log.Summary != nil {
			row["summary"] = log.Summary
		}
		inputLogs = append(inputLogs, row)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/stats.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_STATS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "logs": inputLogs}))
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
	for i, log := range logs {
		value, err := LogMeta(log, StatsOptions{ParseDate: parseISO, Location: time.UTC})
		if err != nil {
			t.Fatal(i, err)
		}
		var actual map[string]any
		_ = json.Unmarshal(raw(value), &actual)
		if !reflect.DeepEqual(actual, source.Rows[i].Value) {
			t.Errorf("case%d Go%s Source%s", i, raw(actual), raw(source.Rows[i].Value))
		}
	}
}
