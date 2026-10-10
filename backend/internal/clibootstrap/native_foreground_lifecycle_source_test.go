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
)

func TestForegroundPromotionReturnGraceAndParentResultAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	if e := os.MkdirAll(m.options.Native.Temp, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-promotion-lifecycle.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PROMOTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "rows": []map[string]any{{"id": "afast", "pendingMs": 30, "finallyMs": 20}, {"id": "aslow", "pendingMs": 1150, "finallyMs": 10}, {"id": "areject", "pendingMs": 30, "finallyMs": 10, "finallyReject": true}}})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4000s", e, out)
	}
	var source struct {
		Rows []struct {
			ID     string
			Result struct {
				Data struct {
					Status, AgentID string
					IsAsync         bool
				}
			}
			Events []struct {
				Name    string
				Elapsed float64
			}
			Task     struct{ Status string }
			Messages []struct {
				Message struct{ Content []struct{ Text string } }
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != 3 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for _, row := range source.Rows {
		if row.Result.Data.Status != "async_launched" || !row.Result.Data.IsAsync || row.Result.Data.AgentID != row.ID || row.Task.Status != "completed" || len(row.Messages) != 1 || row.Messages[0].Message.Content[0].Text != "native background response" {
			t.Fatalf("Source lifecycle identity %s %.4000s", row.ID, out)
		}
		order := map[string]int{}
		elapsed := map[string]float64{}
		for i, event := range row.Events {
			order[event.Name] = i
			elapsed[event.Name] = event.Elapsed
		}
		if order["parent-tool-return"] >= order["background-start"] || elapsed["parent-tool-return"] > 250 {
			t.Fatalf("parent blocked on iterator close %s %+v", row.ID, row.Events)
		}
		if row.ID == "aslow" {
			if order["background-start"] >= order["old-finally-done"] || elapsed["background-start"] < 950 || elapsed["background-start"] > 1120 {
				t.Fatalf("Source one-second grace %s %+v", row.ID, row.Events)
			}
		} else if order["old-finally-done"] >= order["background-start"] || elapsed["background-start"] > 500 {
			t.Fatalf("Source awaited real finally %s %+v", row.ID, row.Events)
		}
	}
}
