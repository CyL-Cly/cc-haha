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

func TestBoundNativeBashProgressUsesOriginalSourceNormalizerAndRejectsForeignShapes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	row := object([]byte(`{"type":"progress","data":{"type":"bash_progress","output":"original last output","fullOutput":"original full output","elapsedTimeSeconds":3,"totalLines":4,"totalBytes":24,"taskId":"b0123abcd"},"toolUseID":"bash-progress-0","parentToolUseID":"original-bash","uuid":"original-progress","timestamp":"2026-10-07T00:00:00.000Z"}`))
	timeout := row.Clone()
	data, _ := timeout.Object("data")
	data.Set("timeoutMs", raw(5000))
	timeout.Set("data", raw(data))
	rows := []string{string(raw(row)), string(raw(timeout))}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "progress": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3500s", e, out)
	}
	var source struct {
		ProgressNormalized [][]json.RawMessage
		Capabilities       struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.ProgressNormalized) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.3500s", e, out)
	}
	factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
	normalizer, e := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return quote("fixture-session") }, NowMillis: func() float64 { return 1700000000000 }, OriginalEnv: func(string) string { return "" }})
	if e != nil {
		t.Fatal(e)
	}
	for i, value := range rows {
		record := object([]byte(value))
		if !retainedNativeBashProgress(record) || !messages.IsNativeBashProgress(record) {
			t.Fatal("actual native producer rejected")
		}
		decoded, e := sdkoutput.Decode([]byte(value))
		if e != nil {
			t.Fatal(e)
		}
		views := []*sdkoutput.View{}
		e = normalizer.NormalizeMessage(decoded, func(view *sdkoutput.View) error { views = append(views, view); return nil })
		if e != nil || len(views) != 0 || len(source.ProgressNormalized[i]) != 0 {
			t.Fatalf("Source local progress was fabricated as SDKevent: %v/%v %s", views, e, source.ProgressNormalized[i])
		}
	}
	for _, entry := range []struct {
		key   string
		value json.RawMessage
		top   bool
	}{{"type", quote("unknown_progress"), false}, {"taskId", quote("foreign-task"), false}, {"totalBytes", quote("24"), false}, {"elapsedTimeSeconds", raw(-1), false}, {"output", raw(nil), false}, {"foreign", raw(true), false}, {"toolUseID", quote("task-output-waiting-1"), true}, {"parentToolUseID", nil, true}} {
		invalid := row.Clone()
		if entry.top {
			if entry.value == nil {
				invalid.Delete(entry.key)
			} else {
				invalid.Set(entry.key, entry.value)
			}
		} else {
			data, _ := invalid.Object("data")
			data.Set(entry.key, entry.value)
			invalid.Set("data", raw(data))
		}
		if retainedNativeBashProgress(invalid) || messages.IsNativeBashProgress(invalid) {
			t.Fatalf("foreign progress widened retention guard %s", raw(invalid))
		}
	}
}
