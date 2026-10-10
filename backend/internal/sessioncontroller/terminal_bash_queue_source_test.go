package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTerminalBashQueueOriginalIsolationAndModeBoundaries(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := [][]map[string]string{
		{{"value": "bash1", "mode": "bash"}, {"value": "bash2", "mode": "bash"}},
		{{"value": "bash1", "mode": "bash"}, {"value": "prompt1", "mode": "prompt"}, {"value": "prompt2", "mode": "prompt"}},
		{{"value": "prompt1", "mode": "prompt"}, {"value": "prompt2", "mode": "prompt"}, {"value": "bash1", "mode": "bash"}},
		{{"value": "notify1", "mode": "task-notification"}, {"value": "notify2", "mode": "task-notification"}, {"value": "bash1", "mode": "bash"}},
		{{"value": "child", "mode": "bash", "agentId": "a-private-owner"}, {"value": "bash1", "mode": "bash"}, {"value": "prompt1", "mode": "prompt"}},
	}

	rows = append(rows,
		[]map[string]string{{"value": "prompt-later", "mode": "prompt", "priority": "later"}, {"value": "bash-next", "mode": "bash", "priority": "next"}, {"value": "prompt-now", "mode": "prompt", "priority": "now"}},
		[]map[string]string{{"value": "prompt1", "mode": "prompt"}, {"value": "notify1", "mode": "task-notification"}, {"value": "prompt2", "mode": "prompt"}, {"value": "notify2", "mode": "task-notification"}},
		[]map[string]string{{"value": "/cost", "mode": "prompt"}, {"value": "prompt1", "mode": "prompt"}, {"value": "/other", "mode": "prompt"}, {"value": "prompt2", "mode": "prompt"}},
		[]map[string]string{{"value": "child-now", "mode": "prompt", "priority": "now", "agentId": "a-private-owner"}, {"value": "bash1", "mode": "bash"}, {"value": "prompt1", "mode": "prompt"}},
	)
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-bash-queue.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TERMINAL_BASH_QUEUE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source queue %v %.2500s", e, out)
	}
	var source struct {
		Rows []struct {
			Batches   [][]struct{ Value, Mode, AgentID string }
			Remaining []struct{ Value, Mode, AgentID string }
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source queue shape %v %.2500s", e, out)
	}
	for i, row := range rows {
		c := activeController(t)
		c.options.NativeInteractiveQueue = true
		modeByValue := map[string]string{}
		c.inputProcessor = &InputProcessor{ProcessQueuedBatch: func(_ *cooperative.Scope, commands []QueuedInput) (*ProcessedInput, error) {
			messages := []protocol.JSONObject{}
			for _, command := range commands {
				messages = append(messages, orderedUser(c.options.Factory.CreateUser(map[string]json.RawMessage{"content": command.Content, "uuid": command.UUID})))
			}
			return &ProcessedInput{Messages: messages}, nil
		}, ProcessTerminalBash: func(_ *cooperative.Scope, content json.RawMessage) (*ProcessedInput, error) {
			message := orderedUser(c.options.Factory.CreateUser(map[string]json.RawMessage{"content": content}))
			return &ProcessedInput{Messages: []protocol.JSONObject{message}, NoQuery: true}, nil
		}, ProcessQueued: func(_ *cooperative.Scope, content, uuid json.RawMessage, _, _ bool, _ json.RawMessage) (*ProcessedInput, error) {
			message := orderedUser(c.options.Factory.CreateUser(map[string]json.RawMessage{"content": content}))
			return &ProcessedInput{Messages: []protocol.JSONObject{message}}, nil
		}}
		for _, commandRow := range row {
			modeByValue[commandRow["value"]] = commandRow["mode"]
			if e = c.enqueue(command{Content: quote(commandRow["value"]), Mode: commandRow["mode"], Priority: func() string {
				if commandRow["priority"] != "" {
					return commandRow["priority"]
				}
				return "next"
			}(), AgentID: commandRow["agentId"]}); e != nil {
				t.Fatal(e)
			}
		}
		batches := [][]string{}
		for {
			before := len(c.messages)
			message, e := c.AcceptNext()
			if e != nil {
				t.Fatal(e)
			}
			if message == nil {
				break
			}
			batch := []string{}
			for _, message := range c.messages[before:] {
				batch = append(batch, object(message.Get("message")).String("content"))
			}
			if modeByValue[batch[0]] == "bash" && len(batch) != 1 {
				t.Fatal("Bash commands combined", batch)
			}
			batches = append(batches, batch)
		}
		expected := [][]string{}
		for _, sourceBatch := range source.Rows[i].Batches {
			batch := []string{}
			for _, command := range sourceBatch {
				batch = append(batch, command.Value)
			}
			expected = append(expected, batch)
		}
		if !reflect.DeepEqual(batches, expected) {
			t.Errorf("case%d Go%v Source%v", i, batches, expected)
		}
		if len(c.queue) != len(source.Rows[i].Remaining) {
			t.Error("subagent queue ownership lost", i)
		}
	}
}
