package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceModelResult struct {
	Value   json.RawMessage
	Queries []struct {
		Prompt    json.RawMessage
		MaxTokens float64
	}
	Errors []string
	Error  string
}

func sourceReport(t *testing.T, input map[string]any) sourceModelResult {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, _ := input["home"].(string)
	if home == "" {
		home = t.TempDir()
	}
	input["home"] = home
	fixture, _ := filepath.Abs("testdata/models-report.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_REPORT_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(raw(input))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var out sourceModelResult
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err, string(data))
	}
	return out
}
func modelLog() Log {
	return Log{SessionID: "source-session-643", ProjectPath: "/private/test", CreatedMillis: 1767225600000, ModifiedMillis: 1767225720000, Messages: []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"content": "actual initial"})}), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("assistant")}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"content": []map[string]any{{"type": "text", "text": "actual reply"}, {"type": "tool_use", "name": "Read", "input": map[string]any{}}}})})}}
}
func sourceLog(log Log) map[string]any {
	return map[string]any{"sessionId": log.SessionID, "projectPath": log.ProjectPath, "firstPrompt": log.FirstPrompt, "createdMillis": log.CreatedMillis, "modifiedMillis": log.ModifiedMillis, "messages": log.Messages}
}
func equalJSON(t *testing.T, a, b json.RawMessage) {
	t.Helper()
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	if !reflect.DeepEqual(x, y) {
		t.Errorf("Go%s Source%s", a, b)
	}
}
func TestInsightsTranscriptSummarizationAndFacetsUseActualSourcePromptsAndUTF16(t *testing.T) {
	log := modelLog()
	for _, test := range []struct {
		name, action string
		long         bool
		responses    []map[string]string
	}{{"plain", "transcript", false, nil}, {"long", "transcript", true, []map[string]string{{"text": "first chunk exact summary"}, {"error": "actual unavailable"}, {"text": ""}}}, {"facets", "facets", false, []map[string]string{{"text": `prefix {"underlying_goal":"real goal","outcome":"fully_achieved","brief_summary":"result","goal_categories":{},"user_satisfaction_counts":{},"friction_counts":{},"future":{"keep":true}} suffix`}}}, {"invalid facets", "facets", false, []map[string]string{{"text": `{"outcome":"only"}`}}}, {"failed facets", "facets", false, []map[string]string{{"error": "actual unavailable"}}}} {
		t.Run(test.name, func(t *testing.T) {
			current := log
			if test.long {
				current.Messages = append([]protocol.JSONObject{}, log.Messages...)
				for i := 0; i < 70; i++ {
					record := log.Messages[0].Clone()
					record.Set("message", raw(map[string]string{"content": strings.Repeat("🌍", 300)}))
					current.Messages = append(current.Messages, record)
				}
			}
			source := sourceReport(t, map[string]any{"action": test.action, "logs": []map[string]any{sourceLog(current)}, "responses": test.responses})
			requests := []ModelRequest{}
			errorsSeen := []string{}
			index := 0
			models, err := NewModels(ModelOptions{Stats: StatsOptions{ParseDate: parseISO, Location: time.UTC}, LogError: func(err error) { errorsSeen = append(errorsSeen, err.Error()) }, QueryBatch: func(ctx context.Context, batch []ModelRequest) ([]ModelResponse, error) {
				out := make([]ModelResponse, len(batch))
				for i, request := range batch {
					requests = append(requests, request)
					if index < len(test.responses) {
						row := test.responses[index]
						if row["error"] != "" {
							out[i].Err = errors.New(row["error"])
						} else {
							out[i].Text = raw(row["text"])
						}
					} else {
						out[i].Text = raw("")
					}
					index++
				}
				return out, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			var value json.RawMessage
			if test.action == "transcript" {
				value, err = models.Transcript(context.Background(), current)
			} else {
				var facets []*protocol.JSONObject
				facets, err = models.Facets(context.Background(), []Log{current})
				value = raw(facets)
			}
			if err != nil {
				t.Fatal(err)
			}
			equalJSON(t, value, source.Value)
			if len(requests) != len(source.Queries) {
				t.Fatal("query count", len(requests), len(source.Queries))
			}
			for i, request := range requests {
				if request.MaxTokens != source.Queries[i].MaxTokens {
					t.Error(i, "maxTokens")
				}
				equalJSON(t, request.Prompt, source.Queries[i].Prompt)
			}
			if !reflect.DeepEqual(errorsSeen, source.Errors) {
				t.Error(errorsSeen, source.Errors)
			}
		})
	}
}
func TestInsightsParallelSectionsThenAtAGlanceAndWholeHTMLMatchActualSource(t *testing.T) {
	data, err := Aggregate([]protocol.JSONObject{}, map[string]protocol.JSONObject{}, parseISO)
	if err != nil {
		t.Fatal(err)
	}
	data.Set("total_messages", raw(12345))
	data.Set("total_lines_added", raw(1999))
	data.Set("total_lines_removed", raw(55))
	data.Set("tool_counts", raw(map[string]int{"Read": 5, "Bash": 2}))
	data.Set("message_hours", raw([]int{0, 6, 6, 12, 18}))
	data.Set("user_response_times", raw([]int{3, 10, 30, 60, 120, 300, 950}))
	data.Set("total_sessions_scanned", raw(100))
	data.Set("multi_clauding", raw(map[string]int{"overlap_events": 2, "sessions_involved": 3, "user_messages_during": 15}))
	responses := []map[string]string{{"text": `{"areas":[{"name":"<private>&'\"","session_count":2,"description":"actual project"}]}`}, {"text": `{"narrative":"**Actual** work\n\n- with tools","key_pattern":"Read & test"}`}, {"text": `{"intro":"wins","impressive_workflows":[{"title":"test","description":"done"}]}`}, {"text": `{"intro":"friction","categories":[{"category":"wrong","description":"fixed","examples":["<example>"]}]}`}, {"text": `{"claude_md_additions":[{"addition":"real rule","why":"useful","where":"root"}],"features_to_try":[{"feature":"MCP","one_liner":"try","why_for_you":"real","example_code":"<code>"}],"usage_patterns":[{"title":"pattern","suggestion":"use","detail":"details","copyable_prompt":"do work"}]}`}, {"text": `{"intro":"horizon","opportunities":[{"title":"idea","whats_possible":"more","how_to_try":"start","copyable_prompt":"read"}]}`}, {"text": `{"headline":"real memorable","detail":"tested"}`}, {"text": `{"whats_working":"**real** <work>","whats_hindering":"friction","quick_wins":"try skills","ambitious_workflows":"do more"}`}}
	facet := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: raw("real")}, protocol.JSONField{Name: "brief_summary", Value: raw("real summary")}, protocol.JSONField{Name: "outcome", Value: raw("fully_achieved")}, protocol.JSONField{Name: "claude_helpfulness", Value: raw("essential")}, protocol.JSONField{Name: "friction_detail", Value: raw("real friction")}, protocol.JSONField{Name: "user_instructions_to_claude", Value: raw([]string{"verify"})})
	source := sourceReport(t, map[string]any{"action": "narrative", "data": data, "facets": []protocol.JSONObject{facet}, "responses": responses})
	requests := []ModelRequest{}
	batches := []int{}
	index := 0
	models, _ := NewModels(ModelOptions{Stats: StatsOptions{ParseDate: parseISO}, LogError: func(err error) { t.Error(err) }, QueryBatch: func(ctx context.Context, batch []ModelRequest) ([]ModelResponse, error) {
		batches = append(batches, len(batch))
		out := make([]ModelResponse, len(batch))
		for i, request := range batch {
			requests = append(requests, request)
			out[i].Text = raw(responses[index]["text"])
			index++
		}
		return out, nil
	}})
	result, err := models.Narrative(context.Background(), data, []protocol.JSONObject{facet})
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, raw(result), source.Value)
	if !reflect.DeepEqual(batches, []int{7, 1}) {
		t.Fatal(batches)
	}
	for i, request := range requests {
		equalJSON(t, request.Prompt, source.Queries[i].Prompt)
		if request.MaxTokens != source.Queries[i].MaxTokens {
			t.Error("maxTokens", i)
		}
	}
	for _, insight := range []protocol.JSONObject{protocol.NewJSONObject(), result} {
		source := sourceReport(t, map[string]any{"action": "html", "data": data, "insights": insight})
		html, err := HTML(data, insight)
		if err != nil {
			t.Fatal(err)
		}
		var expected string
		_ = json.Unmarshal(source.Value, &expected)
		if html != expected {
			file := filepath.Join(t.TempDir(), "actual.html")
			_ = os.WriteFile(file, []byte(html), 0600)
			_ = os.WriteFile(file+".source", []byte(expected), 0600)
			t.Errorf("complete HTML differs: %s (Go%d Source%d)", file, len(html), len(expected))
		}
	}
}
