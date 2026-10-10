package application

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
)

func TestStreamRecoveryProjectionMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := [][]json.RawMessage{}
	for _, cause := range []string{`"watchdog"`, `"stream_error"`, `"404_stream_creation"`, `"stream_retry"`, `"future_cause"`, `null`, `7`} {
		rows = append(rows, []json.RawMessage{
			json.RawMessage(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"same"}}}`),
			json.RawMessage(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`),
			json.RawMessage(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"discarded"}}}`),
			json.RawMessage(`{"type":"system","subtype":"streaming_fallback","cause":` + cause + `}`),
			json.RawMessage(`{"type":"assistant","message":{"id":"same","content":[{"type":"text","text":"recovered"}]}}`),
		})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-stream-projection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_STREAM_PROJECTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, err := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Frames       [][][]json.RawMessage
		State        map[string]any
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Frames) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.500s", err, output)
	}
	for i, row := range rows {
		p := NewProjection()
		for j, message := range row {
			got, err := p.Translate(message)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			_ = json.Unmarshal(raw(got), &a)
			_ = json.Unmarshal(raw(source.Frames[i][j]), &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("row%d step%d Source%s Go%s", i, j, raw(b), raw(a))
			}
		}
	}
	// Source resets stream bookkeeping only, retaining the error and pending command.
	p := NewProjection()
	p.LiveUserTurn = true
	p.Stopped = true
	p.lastError = "retained error"
	command := new(localCommand)
	p.localCommand = command
	p.agentRuns["worker"] = NewProjection()
	project(t, p, `{"type":"system","subtype":"streaming_fallback","cause":"stream_retry"}`)
	if source.State["lastApiError"].(map[string]any)["message"] != p.lastError || !p.LiveUserTurn || !p.Stopped || p.localCommand != command || p.agentRuns["worker"] == nil {
		t.Fatal("recovery cleared state outside original stream attempt")
	}
}
