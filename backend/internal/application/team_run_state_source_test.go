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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopOriginalSDKRunStateProjectionAndReconnectAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []protocol.JSONObject{}
	for _, value := range []string{
		`{"type":"system","subtype":"session_state_changed","state":"running"}`,
		`{"type":"system","subtype":"session_state_changed","state":"invalid","message":"retained"}`,
		`{"type":"system","subtype":"other","state":"idle"}`,
		`{"type":"system","subtype":"session_state_changed","state":"idle","message":null}`,
		`{"type":"system","subtype":"session_state_changed","state":"running","message":"😀"}`,
		`{"type":"system","subtype":"session_state_changed","state":null}`,
		`{"type":"system","subtype":"session_state_changed","state":"idle"}`,
	} {
		rows = append(rows, obj([]byte(value)))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-run-state.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_RUN_STATE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Running    bool
			Projection []protocol.JSONObject
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	frames := []json.RawMessage{}
	d := &DesktopSession{projection: NewProjection(), emit: func(data json.RawMessage) { frames = append(frames, data) }}
	for i, row := range rows {
		frames = nil
		if err = d.output(raw(row)); err != nil {
			t.Fatal(err)
		}
		got := []protocol.JSONObject{}
		for _, frame := range frames {
			got = append(got, obj(frame))
		}
		var a, b any
		_ = json.Unmarshal(raw(got), &a)
		_ = json.Unmarshal(raw(source.Rows[i].Projection), &b)
		if d.cliRunning != source.Rows[i].Running || !reflect.DeepEqual(a, b) {
			t.Fatalf("row %d: SDK state/projection differs; %s vs %s", i, raw(got), raw(source.Rows[i]))
		}
	}
}
