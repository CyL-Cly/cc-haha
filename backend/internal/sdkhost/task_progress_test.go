package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskOutputProgressUsesActualSourceNormalizerWithoutFabricatedSDKEvent(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	rows := []string{`{"type":"progress","data":{"type":"waiting_for_task","taskDescription":"actual task","taskType":"local_bash"},"toolUseID":"task-output-waiting-1700000000000","parentToolUseID":"original-task-output","uuid":"original-progress","timestamp":"2026-10-06T00:00:00.000Z"}`, `{"type":"progress","data":{"type":"waiting_for_task","taskDescription":"\ud800original","taskType":"local_agent"},"toolUseID":"task-output-waiting-1","parentToolUseID":"original-task-output","uuid":"original-progress","timestamp":"2026-10-06T00:00:00.000Z"}`}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "progress": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.1600s", e, out)
	}
	var source struct {
		ProgressNormalized [][]json.RawMessage
		Capabilities       struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.ProgressNormalized) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.1600s", e, out)
	}
	factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
	normalizer, e := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return quote("fixture-session") }, NowMillis: func() float64 { return 1700000000000 }, OriginalEnv: func(string) string { return "" }})
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		record := object([]byte(row))
		if !retainedTaskOutputProgress(record) {
			t.Fatal("actual TaskOutput producer shape rejected")
		}
		message, e := sdkoutput.Decode([]byte(row))
		if e != nil {
			t.Fatal(e)
		}
		got := []*sdkoutput.View{}
		e = normalizer.NormalizeMessage(message, func(view *sdkoutput.View) error { got = append(got, view); return nil })
		if e != nil || len(got) != 0 || len(source.ProgressNormalized[i]) != 0 {
			t.Errorf("progress%d Go%v/%v Source%v", i, got, e, source.ProgressNormalized[i])
		}
	}
	for _, row := range []string{`{"toolUseID":"task-output-waiting-1","data":{"type":"unported_progress","taskDescription":"x","taskType":"local_bash"}}`, `{"toolUseID":"foreign-id","data":{"type":"waiting_for_task","taskDescription":"x","taskType":"local_bash"}}`, `{"toolUseID":"task-output-waiting-1","data":{"type":"waiting_for_task","taskDescription":null,"taskType":"local_bash"}}`} {
		if retainedTaskOutputProgress(object([]byte(row))) {
			t.Fatal("task proof widened unknown progress guard")
		}
	}
}
