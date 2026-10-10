package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/insights"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type nativeInsightsFiles struct{ n *NativeBoundary }

func (f nativeInsightsFiles) ReadFile(path string) ([]byte, error) { return f.n.readFile(path) }
func (f nativeInsightsFiles) MkdirAll(path string, mode fs.FileMode) error {
	if err := f.n.check("insights-mkdir", path); err != nil {
		return err
	}
	return os.MkdirAll(path, mode)
}
func (f nativeInsightsFiles) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := f.n.check("insights-write", path); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
func (f nativeInsightsFiles) Remove(path string) error {
	if err := f.n.check("insights-remove", path); err != nil {
		return err
	}
	return os.Remove(path)
}

var insightsUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (m *MainPrint) nativeInsightsCommand() (skillcommands.Definition, error) {
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("prompt")}, protocol.JSONField{Name: "name", Value: quoteMain("insights")}, protocol.JSONField{Name: "description", Value: quoteMain("Generate a report analyzing your Claude Code sessions")}, protocol.JSONField{Name: "contentLength", Value: rawMain(0)}, protocol.JSONField{Name: "progressMessage", Value: quoteMain("analyzing your sessions")}, protocol.JSONField{Name: "source", Value: quoteMain("builtin")})
	return skillcommands.NewNativeCommand(metadata, func(ctx context.Context, args *string, invocation skillcommands.Invocation) (string, error) {
		scope := invocation.NativeScope
		if !scope.BelongsTo(m.Owner) || m.Host == nil || m.insightsHistory == nil {
			return "", cooperative.ErrRestricted
		}
		n := m.options.Native
		if n.Environment["USER_TYPE"] == "ant" {
			return "", mainCap("Insights internal remote homespaces and report upload")
		}
		builtins, err := commandnames.Set(n.Environment)
		if err != nil {
			return "", err
		}
		config := n.Environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(n.Home, ".claude")
		}
		if !filepath.IsAbs(config) {
			config = filepath.Join(n.CWD, config)
		}
		cache, err := insights.NewCache(config, nativeInsightsFiles{n})
		if err != nil {
			return "", err
		}
		date := activitystatsapi.NewIndexDateParser(time.Local)
		parse := func(value string) (float64, error) {
			stamp, valid, err := date.Parse(value)
			if !valid {
				stamp = math.NaN()
			}
			return stamp, err
		}
		stats := insights.StatsOptions{ParseDate: parse, Location: time.Local}
		models, err := insights.NewModels(insights.ModelOptions{Stats: stats, LogError: func(err error) { _, _ = fmt.Fprintf(m.options.Error, "%v\n", err) }, QueryBatch: func(_ context.Context, requests []insights.ModelRequest) ([]insights.ModelResponse, error) {
			settings, err := m.Startup.GetInitialSettings()
			if err != nil {
				return nil, err
			}
			env := map[string]string{}
			for _, field := range m.environment.Snapshot().OwnProperties() {
				var key, value string
				_ = json.Unmarshal(field.NameJSON, &key)
				_ = json.Unmarshal(field.Value, &value)
				env[key] = value
			}
			selector, err := modelselection.NewParsing(modelselection.Options{Environment: env, Settings: settings.Clone()})
			if err != nil {
				return nil, err
			}
			model, err := selector.Parse(quoteMain("opus"))
			if err != nil {
				return nil, err
			}
			var modelName string
			_ = json.Unmarshal(model, &modelName)
			batch := make([]sdkhost.InsightsRequest, len(requests))
			for i, request := range requests {
				batch[i] = sdkhost.InsightsRequest{Prompt: request.Prompt, Model: modelName, MaxTokens: request.MaxTokens}
			}
			// Original Insights creates a fresh AbortController for each call;
			// cancelling the main turn does not abort its analysis requests.
			results, err := m.Host.QueryInsightsBatch(context.Background(), scope, batch)
			if err != nil {
				return nil, err
			}
			out := make([]insights.ModelResponse, len(results))
			for i, result := range results {
				out[i] = insights.ModelResponse{Text: result.Text, Err: result.Err}
			}
			return out, nil
		}})
		if err != nil {
			return "", err
		}
		comparer := collate.New(language.English)
		reports, err := insights.NewReports(insights.ReportOptions{AwaitFiles: func(_ context.Context, work func() ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.TranscriptOpenStat, func(context.Context) ([]byte, error) { return work() })
		}, Cache: cache, Models: models, Stats: stats, Compare: func(a, b string) (int, error) { return comparer.CompareString(a, b), nil }, Scan: func(context.Context) ([]insights.SessionFile, error) {
			entries, err := n.readDir(filepath.Join(config, "projects"))
			if err != nil {
				return []insights.SessionFile{}, nil
			}
			files := []insights.SessionFile{}
			count := 0
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				project := filepath.Join(config, "projects", entry.Name())
				sessions, err := n.readDir(project)
				if err == nil {
					for _, session := range sessions {
						if !session.Type().IsRegular() || !strings.HasSuffix(session.Name(), ".jsonl") {
							continue
						}
						id := strings.TrimSuffix(session.Name(), ".jsonl")
						if !insightsUUID.MatchString(id) {
							continue
						}
						path := filepath.Join(project, session.Name())
						stat, err := n.stat(path)
						if err != nil {
							continue
						}
						files = append(files, insights.SessionFile{SessionID: id, Path: path, Mtime: float64(stat.ModTime().UnixMilli()), Size: stat.Size()})
					}
				}
				count++
				if count%10 == 0 {
					if err := scope.AwaitImmediate(); err != nil {
						return nil, err
					}
				}
			}
			sort.SliceStable(files, func(i, j int) bool { return files[i].Mtime > files[j].Mtime })
			return files, nil
		}, Load: func(_ context.Context, path string) ([]insights.Log, error) {
			logs, err := m.insightsHistory.AllLogs(path, sessionhistory.AllLogsOptions{FirstPrompt: func(records []protocol.JSONObject) (json.RawMessage, error) {
				return insights.FirstPrompt(records, builtins)
			}})
			if err != nil {
				return nil, err
			}
			out := make([]insights.Log, len(logs))
			for i, log := range logs {
				var id, project, first string
				_ = json.Unmarshal(log.SessionID, &id)
				_ = json.Unmarshal(log.ProjectPath, &project)
				_ = json.Unmarshal(log.FirstPrompt, &first)
				out[i] = insights.Log{SessionID: id, ProjectPath: project, FirstPrompt: first, Summary: log.Summary, CreatedMillis: log.CreatedMillis, ModifiedMillis: log.ModifiedMillis, Messages: log.Messages}
			}
			return out, nil
		}})
		if err != nil {
			return "", err
		}
		report, err := reports.Generate(ctx)
		if err != nil {
			return "", err
		}
		return insights.CommandPrompt(report, cache.FacetsDir()), nil
	})
}
