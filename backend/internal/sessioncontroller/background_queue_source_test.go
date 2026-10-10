package sessioncontroller

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
)

func TestRootCommandDrainMatchesActualSourceAgentOwnershipAndPriority(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := [][]map[string]string{
		{{"value": "agent-now", "priority": "now", "agentId": "a-owner"}},
		{{"value": "agent-now", "priority": "now", "agentId": "a-owner"}, {"value": "root-later", "priority": "later"}, {"value": "agent-next", "priority": "next", "agentId": "b-owner"}, {"value": "root-now", "priority": "now"}, {"value": "root-next", "priority": "next"}},
		{{"value": "root-next1", "priority": "next"}, {"value": "agent-next", "priority": "next", "agentId": "a-owner"}, {"value": "root-next2", "priority": "next"}},
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "partitions": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.1800s", e, out)
	}
	var source struct {
		Partitions []struct {
			Root      []string
			Remaining []map[string]string
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Partitions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.1800s", e, out)
	}
	for i, commands := range rows {
		controller := activeController(t)
		for _, row := range commands {
			if e = controller.enqueue(command{Content: quote(row["value"]), UUID: quote(row["value"]), Priority: row["priority"], AgentID: row["agentId"]}); e != nil {
				t.Fatal(e)
			}
		}
		actual := []string{}
		for {
			message, e := controller.AcceptNext()
			if e != nil {
				t.Fatal(e)
			}
			if message == nil {
				break
			}
			actual = append(actual, strings.Split(object(message.Get("message")).String("content"), "\n")...)
		}
		if !reflect.DeepEqual(actual, source.Partitions[i].Root) {
			t.Fatalf("root drain%d Go%v Source%v", i, actual, source.Partitions[i].Root)
		}
		remaining := []map[string]string{}
		for _, item := range controller.queue {
			remaining = append(remaining, map[string]string{"value": text(item.Content), "priority": item.Priority, "agentId": item.AgentID})
		}
		if !reflect.DeepEqual(remaining, source.Partitions[i].Remaining) {
			t.Fatalf("owned notifications were stolen%d Go%v Source%v", i, remaining, source.Partitions[i].Remaining)
		}
	}
}
