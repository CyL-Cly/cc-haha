package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceSDKAgentSummaryTruthyValuesReadDiskDenyToolsAndEmitProgress(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture := os.Getenv("CC_HAHA_AGENT_SUMMARY_SOURCE")
	if fixture == "" {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-agent-summaries.fixture.ts")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_AGENT_SUMMARY_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-summary", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_MAX_RETRIES=0", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader([]byte(`{}`))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source 30-second summary", err, stderr.String())
	}
	t.Logf("actual_agent_summary_source_evidence=%s", data)
	var source struct {
		Controls []struct {
			Name    string
			Enabled bool
			Ack     []string
		}
		Rows []struct {
			Name, Summary                                                                            string
			Event                                                                                    protocol.JSONObject
			Requests                                                                                 int
			ToolDenied, ToolsAdvertised, FreshDisk, ParentUnchanged, ParentAborted, PreviousConsumed bool
			CacheMarkers                                                                             [][]int
			MessageCounts                                                                            []int
		}
		FileExists   bool
		Capabilities protocol.JSONObject
	}
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	if len(source.Controls) != 9 || len(source.Rows) != 5 || source.FileExists || string(source.Capabilities.Get("blocked")) != "[]" {
		t.Fatal("actual Source scope", string(data))
	}
	for i, row := range source.Controls {
		if row.Enabled != (i < 5) || len(row.Ack) != 1 || row.Ack[0] != "success" {
			t.Fatal("Source runtime flag truthiness", i, string(rawMain(row)))
		}
	}
	for index, row := range source.Rows {
		expectedText, expectedRequests := "Reading summary-target.ts", 2
		if index == 0 {
			expectedText, expectedRequests = "Fixing summary-target.ts", 3
		}
		if index == 1 {
			expectedRequests = 3
		}
		if row.Summary != expectedText || row.Requests != expectedRequests || !row.PreviousConsumed || !row.ToolDenied || !row.ToolsAdvertised || !row.FreshDisk || !row.ParentUnchanged || row.ParentAborted {
			t.Fatal("Source actual summary producer", string(rawMain(row)))
		}
		if row.Event.String("subtype") != "task_progress" || row.Event.String("description") != row.Summary || row.Event.String("summary") != row.Summary || row.Event.String("tool_use_id") != "tool-"+row.Name || row.Event.String("owner_agent_id") != "owner-"+row.Name {
			t.Fatal("Source SDK consumer", string(rawMain(row.Event)))
		}
		usage, _ := row.Event.Object("usage")
		if string(usage.Get("total_tokens")) != "19" || string(usage.Get("tool_uses")) != "2" {
			t.Fatal("Source summary replaced task counters", string(rawMain(row.Event)))
		}
		for i, markers := range row.CacheMarkers {
			if len(markers) != 1 || markers[0] != row.MessageCounts[i]-1 {
				t.Fatal("Source summary cache writes remain enabled", markers, row.MessageCounts[i])
			}
		}
	}
}
