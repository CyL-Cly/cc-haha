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

func TestActualSourceWholeAttachmentGateRetainsOrDrainsRunningAgentMessages(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-pending-attachment-gate.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cases := []map[string]string{{"disabled": "1", "simple": "0"}, {"disabled": "0", "simple": "0"}, {"disabled": "0", "simple": "1"}, {"disabled": "false", "simple": "false"}}
	payload, _ := json.Marshal(map[string]any{"home": home, "cases": cases})
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_PENDING_GATE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=" + home}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, e := command.Output()
	if e != nil {
		t.Fatal("actual original full attachments", e, string(output), stderr.String())
	}
	var source struct {
		Results []struct {
			Disabled, Simple string
			Pending          []string
			Attachments      []protocol.JSONObject
		}
		Blocked []string
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Results) != len(cases) || len(source.Blocked) != 0 {
		t.Fatal("actual original attachment receipt", e, string(output))
	}
	t.Log("whole original getAttachments", string(output))
	for i, row := range source.Results {
		if row.Disabled != cases[i]["disabled"] || row.Simple != cases[i]["simple"] {
			t.Fatal("original gate inputs changed", i, row)
		}
		disabled := i == 0 || i == 2
		if disabled {
			if len(row.Pending) != 1 || row.Pending[0] != "ACTUAL_PENDING_GATE_C7A2" || len(row.Attachments) != 0 {
				t.Fatal("original disabled gate consumed pending", i, row)
			}
			continue
		}
		if len(row.Pending) != 0 || len(row.Attachments) != 1 {
			t.Fatal("original enabled gate did not drain once", i, row)
		}
		value := row.Attachments[0]
		origin, _ := value.Object("origin")
		if value.String("type") != "queued_command" || value.String("prompt") != "ACTUAL_PENDING_GATE_C7A2" || origin.String("kind") != "coordinator" || string(value.Get("isMeta")) != "true" {
			t.Fatal("original coordinator attachment changed", i, value)
		}
	}
}
