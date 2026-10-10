package insights

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInsightsOriginalReportColdWarmCacheFilteringAndCommandPrompt(t *testing.T) {
	for _, action := range []string{"report", "command"} {
		t.Run(action, func(t *testing.T) {
			sourceHome, goHome := t.TempDir(), t.TempDir()
			log := modelLog()
			log.Messages = append(log.Messages, log.Messages[0].Clone())
			scanned := []SessionFile{{SessionID: log.SessionID, Path: "private-first"}}
			// New report facade consumes the original canonical log/cache producers;
			// the Source function below uses disposable physical cache files too.
			response := map[string]string{"text": `{"underlying_goal":"real","goal_categories":{"implement_feature":1},"outcome":"fully_achieved","brief_summary":"actual","user_satisfaction_counts":{},"friction_counts":{},"future":{"keep":true}}`}
			responses := []map[string]string{response}
			for i := 0; i < 8; i++ {
				responses = append(responses, map[string]string{"text": "{}"})
			}
			index := 0
			requests := []ModelRequest{}
			models, _ := NewModels(ModelOptions{Stats: StatsOptions{ParseDate: parseISO, Location: time.UTC}, LogError: func(err error) { t.Error(err) }, QueryBatch: func(ctx context.Context, batch []ModelRequest) ([]ModelResponse, error) {
				out := make([]ModelResponse, len(batch))
				for i, request := range batch {
					requests = append(requests, request)
					text := "{}"
					if index < len(responses) {
						text = responses[index]["text"]
					}
					out[i] = ModelResponse{Text: raw(text)}
					index++
				}
				return out, nil
			}})
			cache, _ := NewCache(filepath.Join(goHome, ".claude"), physicalCacheFiles{})
			reports, _ := NewReports(ReportOptions{AwaitFiles: func(_ context.Context, work func() ([]byte, error)) ([]byte, error) { return work() }, Cache: cache, Models: models, Stats: StatsOptions{ParseDate: parseISO, Location: time.UTC}, Compare: func(a, b string) (int, error) { return compare(a, b), nil }, Scan: func(context.Context) ([]SessionFile, error) { return scanned, nil }, Load: func(context.Context, string) ([]Log, error) { return []Log{log}, nil }})
			report, err := reports.Generate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			source := sourceReport(t, map[string]any{"home": sourceHome, "action": action, "scanned": []map[string]any{{"sessionId": log.SessionID, "path": "private-first", "mtime": 1, "size": 1}}, "files": map[string]any{"private-first": []map[string]any{sourceLog(log)}}, "responses": responses})
			if source.Error != "" {
				t.Fatal(source.Error, source.Errors)
			}
			if action == "command" {
				var prompt []struct{ Text string }
				_ = json.Unmarshal(source.Value, &prompt)
				actual := CommandPrompt(report, cache.FacetsDir())
				expected := strings.ReplaceAll(prompt[0].Text, sourceHome, goHome)
				if actual != expected {
					t.Errorf("command Go%q Source%q", actual, expected)
				}
			} else {
				var expected struct {
					Insights, Data protocol.JSONObject
					Facets         []protocol.JSONObject
				}
				_ = json.Unmarshal(source.Value, &expected)
				equalJSON(t, raw(report.Insights), raw(expected.Insights))
				equalJSON(t, raw(report.Data), raw(expected.Data))
				equalJSON(t, raw(report.Facets), raw(expected.Facets))
			}
			if len(requests) != 9 || len(source.Queries) != 9 {
				t.Fatal("cold calls", len(requests), len(source.Queries))
			}
			for i, request := range requests {
				equalJSON(t, request.Prompt, source.Queries[i].Prompt)
			}
			for _, part := range []string{"session-meta", "facets"} {
				a, err := os.ReadFile(filepath.Join(goHome, ".claude", "usage-data", part, log.SessionID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(sourceHome, ".claude", "usage-data", part, log.SessionID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				equalJSON(t, a, b)
			}
			a, _ := os.ReadFile(report.HTMLPath)
			b, _ := os.ReadFile(filepath.Join(sourceHome, ".claude", "usage-data", "report.html"))
			if !reflect.DeepEqual(a, b) {
				t.Error("actual file HTML differs")
			}
			requests = nil
			_, err = reports.Generate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(requests) != 8 {
				t.Fatal("warm report incorrectly extracted facets", len(requests))
			}
		})
	}
}
