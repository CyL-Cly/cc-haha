package sessionhistory_test

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

	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/insights"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func TestInsightsAllCanonicalLeafBranchesAndTrailingMessagesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	content := `{"type":"user","uuid":"u","parentUuid":null,"sessionId":"11111111-1111-4111-8111-111111111111","cwd":"/private/project","timestamp":"2026-01-01T00:00:00Z","message":{"content":"real first prompt"}}
{"type":"assistant","uuid":"a1","parentUuid":"u","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:01:00Z","message":{"id":"one","content":[{"type":"text","text":"first branch"}]}}
{"type":"assistant","uuid":"a2","parentUuid":"u","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:02:00Z","message":{"id":"two","content":[{"type":"text","text":"second branch"}]}}
{"type":"summary","leafUuid":"a2","summary":"real summary"}
{"type":"system","subtype":"local_command","uuid":"s","parentUuid":"a2","sessionId":"11111111-1111-4111-8111-111111111111","timestamp":"2026-01-01T00:03:00Z","content":"trailing system"}
`
	home := t.TempDir()
	path := filepath.Join(home, "private.jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: sessionstore.OSFileSystem{}, Stat: func(path string) (int64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: os.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	names, err := commandnames.Set(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := loader.AllLogs(path, sessionhistory.AllLogsOptions{FirstPrompt: func(records []protocol.JSONObject) (json.RawMessage, error) {
		return insights.FirstPrompt(records, names)
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../insights/testdata/all-logs.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INSIGHTS_ALL_LOGS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production"}
	input, _ := json.Marshal(map[string]any{"home": home, "path": path})
	cmd.Stdin = bytes.NewReader(input)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct{ Logs []map[string]any }
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	rows := []map[string]any{}
	for _, log := range actual {
		row := map[string]any{"messages": log.Messages, "sessionId": log.SessionID, "leafUuid": log.LeafUUID, "projectPath": log.ProjectPath, "firstPrompt": log.FirstPrompt, "createdMillis": log.CreatedMillis, "modifiedMillis": log.ModifiedMillis}
		if log.Summary != nil {
			row["summary"] = log.Summary
		}
		rows = append(rows, row)
	}
	output, _ := json.Marshal(rows)
	var observed []map[string]any
	_ = json.Unmarshal(output, &observed)
	if !reflect.DeepEqual(observed, source.Logs) {
		t.Fatalf("Go%s Source%s", output, data)
	}
}
