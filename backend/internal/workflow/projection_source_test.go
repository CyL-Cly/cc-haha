package workflow

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

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func TestActualSourceWorkflowIncrementalProjectionNativePhysicalCache(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type op struct {
		Kind string `json:"kind"`
		Text string `json:"text,omitempty"`
	}
	relevant := `{"type":"user","message":{"role":"user","content":"local_workflow real launch"},"unknown":{"keep":true}}`
	notice := `{"type":"cc-haha-task-notification","taskNotification":{"taskId":"actual","toolUseId":"use","status":"failed"}}`
	unrelated := `{"type":"assistant","message":{"role":"assistant","content":"ordinary body discarded"}}`
	rows := [][]op{{{"write", relevant + "\n" + unrelated + "\n"}, {"read", ""}, {"read", ""}, {"append", notice + "\n"}, {"read", ""}}, {{"write", relevant + "\n" + `{"type":"cc-haha-task-notification"`}, {"read", ""}, {"read", ""}, {"append", `,"taskNotification":{"taskId":"partial"}}` + "\n"}, {"read", ""}}, {{"write", relevant + "\nmalformed\n"}, {"read", ""}, {"append", notice + "\n"}, {"read", ""}, {"replace", notice + "\n"}, {"read", ""}}, {{"write", `["async_launched",{"x":"value"}]` + "\nnull\n" + unrelated + "\n"}, {"read", ""}}, {{"write", `{"payload":"` + strings.Repeat("x", 1024*1024) + ` local_workflow"}` + "\n" + notice + "\n"}, {"read", ""}, {"append", relevant + "\n"}, {"read", ""}}, {{"write", relevant + "\n"}, {"read", ""}, {"write", notice + "\n"}, {"read", ""}}}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "projections": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source projection %v %.2500s %.2500s", e, output.String(), stderr.String())
	}
	var source struct {
		Projections  [][]json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.Projections) != len(rows) {
		t.Fatal(e, source.Capabilities.Blocked, len(source.Projections))
	}
	canonical := func(raw []byte) any {
		var value any
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	reader := NewProjectionReader(nil, sessionstore.NewBudget())
	for i, row := range rows {
		path := filepath.Join(t.TempDir(), "actual.jsonl")
		tick, observation := 0, 0
		previous := ""
		for _, op := range row {
			switch op.Kind {
			case "write":
				if e := os.WriteFile(path, []byte(op.Text), 0600); e != nil {
					t.Fatal(e)
				}
			case "append":
				file, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = file.WriteString(op.Text)
				closeErr := file.Close()
				if e != nil || closeErr != nil {
					t.Fatal(e, closeErr)
				}
			case "replace":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte(op.Text), 0600); e != nil {
					t.Fatal(e)
				}
			case "read":
				result, e := reader.Read(ctx, path)
				if e != nil {
					t.Fatal(e)
				}
				actual, _ := json.Marshal(map[string]any{"messages": result.Messages, "complete": result.Complete, "readBytes": result.ReadBytes, "sourceChanged": previous != "" && previous != result.SourceVersion, "sourceVersionPresent": result.SourceVersion != ""})
				if !reflect.DeepEqual(canonical(actual), canonical(source.Projections[i][observation])) {
					t.Fatalf("Source physical projection %d/%d mismatch\nGo=%s\nSource=%s", i, observation, actual, source.Projections[i][observation])
				}
				previous = result.SourceVersion
				observation++
				continue
			}
			when := time.UnixMilli(1700000000000 + int64(tick))
			tick++
			if e := os.Chtimes(path, when, when); e != nil {
				t.Fatal(e)
			}
		}
	}
}
