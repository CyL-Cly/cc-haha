package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This Source oracle retains the actual initialize handler, SDK flag/emission
// bodies, tryGenerateSuggestion, runForkedAgent, query and loopback HTTP. It
// does not replace the fork, suggestion output, permission or cache producers.
func TestActualSourceSDKPromptSuggestionRuntimeValuesDenyAndSDKEmission(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture := os.Getenv("CC_HAHA_PROMPT_SUGGESTION_SOURCE")
	if fixture == "" {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-prompt-suggestions.fixture.ts")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profiles := []map[string]any{
		{"name": "null", "request": map[string]any{"promptSuggestions": nil, "agentProgressSummaries": nil}},
		{"name": "zero", "request": map[string]any{"promptSuggestions": 0, "agentProgressSummaries": 0}},
		{"name": "empty-string", "request": map[string]any{"promptSuggestions": "", "agentProgressSummaries": ""}},
		{"name": "true", "request": map[string]any{"promptSuggestions": true}, "tryWrite": true},
		{"name": "truthy-string", "request": map[string]any{"promptSuggestions": "false"}},
		{"name": "truthy-number", "request": map[string]any{"promptSuggestions": 1}},
		{"name": "truthy-array", "request": map[string]any{"promptSuggestions": []any{}}},
		{"name": "truthy-object", "request": map[string]any{"promptSuggestions": map[string]any{}}},
		{"name": "filtered", "request": map[string]any{"promptSuggestions": true}, "text": "looks good"},
		{"name": "progress-true", "request": map[string]any{"promptSuggestions": false, "agentProgressSummaries": true}},
		{"name": "progress-one", "request": map[string]any{"promptSuggestions": false, "agentProgressSummaries": 1}},
		{"name": "progress-string", "request": map[string]any{"promptSuggestions": false, "agentProgressSummaries": "false"}},
		{"name": "progress-array", "request": map[string]any{"promptSuggestions": false, "agentProgressSummaries": []any{}}},
		{"name": "progress-object", "request": map[string]any{"promptSuggestions": false, "agentProgressSummaries": map[string]any{}}},
		{"name": "held-result", "request": map[string]any{"promptSuggestions": true}, "held": true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_PROMPT_SUGGESTION_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-suggestion", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_MAX_RETRIES=0", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"suggestions": profiles}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source producer", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Name                                                                                                                     string
			PromptEnabled, ProgressEnabled, ToolsAdvertised, ToolDenied, FileExists, ParentUnchanged, ParentAborted, HeldBeforeFlush bool
			Calls                                                                                                                    int
			Ack                                                                                                                      []string
			Output                                                                                                                   []protocol.JSONObject
			CacheMarkers                                                                                                             [][]int
			MessageCounts                                                                                                            []int
		}
		Capabilities protocol.JSONObject
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(profiles) {
		t.Fatal(err, string(data))
	}
	if string(source.Capabilities.Get("blocked")) != "[]" {
		t.Fatal("Source capability escape", string(rawMain(source.Capabilities)))
	}
	for i, row := range source.Rows {
		if row.Name != profiles[i]["name"] || len(row.Ack) != 1 || row.Ack[0] != "success" || row.ProgressEnabled != (i >= 9 && i < 14) || row.FileExists || !row.ParentUnchanged || row.ParentAborted {
			t.Fatal("Source real producer contract", i, string(rawMain(row)))
		}
		if i < 3 || i >= 9 && i < 14 {
			if row.PromptEnabled || row.Calls != 0 || len(row.Output) != 1 {
				t.Fatal("Source falsy runtime value enabled producer", i, string(rawMain(row)))
			}
			continue
		}
		if i == 14 && !row.HeldBeforeFlush {
			t.Fatal("original Source emitted held suggestion before result release")
		}
		calls := 1
		if i == 3 {
			calls = 2
			if !row.ToolDenied {
				t.Fatal("Source actual tool denial absent")
			}
		}
		outputs := 2
		if i == 8 {
			outputs = 1
		}
		if !row.PromptEnabled || row.Calls != calls || !row.ToolsAdvertised || len(row.Output) != outputs {
			t.Fatal("Source truthy actual fork", i, string(rawMain(row)))
		}
		if outputs == 2 && (row.Output[1].String("type") != "prompt_suggestion" || row.Output[1].String("suggestion") != "run the tests") {
			t.Fatal("Source SDK emission", i, string(rawMain(row.Output)))
		}
		for index, markers := range row.CacheMarkers {
			if len(markers) != 1 || markers[0] != row.MessageCounts[index]-2 {
				t.Fatal("Source skipCacheWrite marker", i, index, markers, row.MessageCounts)
			}
		}
	}
}
