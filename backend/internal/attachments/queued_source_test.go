package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestQueuedCommandsPreserveOriginalMetaOriginAndUTF16(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	rows := []string{`{"type":"queued_command","prompt":"actual human"}`, `{"type":"queued_command","prompt":"\ud800original😀","commandMode":"task-notification"}`, `{"type":"queued_command","prompt":"coordinator task","origin":{"kind":"coordinator"},"source_uuid":"actual-source"}`, `{"type":"queued_command","prompt":"channel task","origin":{"kind":"channel","server":"\ud800peer"}}`, `{"type":"queued_command","prompt":[{"type":"text","text":"one"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"fixture"}},{"type":"text","text":"\ud800two"}],"isMeta":true}`, `{"type":"queued_command","prompt":"typed","origin":{"kind":"human"},"isMeta":false}`}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "queued": rows})
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2500s", e, out)
	}
	var source struct {
		Queued       [][]protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Queued) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.2200s", e, out)
	}
	factory, _ := messages.NewFactory(func() string { return "fixture-id" }, func() time.Time { return time.Unix(0, 0) })
	for i, row := range rows {
		got, e := Project([]byte(row), factory)
		if e != nil {
			t.Fatal(e)
		}
		for j := range got {
			got[j].Delete("uuid")
			got[j].Delete("timestamp")
		}
		actual, _ := json.Marshal(got)
		expected, _ := json.Marshal(source.Queued[i])
		if string(protocol.StringifyValidatedJavaScriptJSON(actual)) != string(protocol.StringifyValidatedJavaScriptJSON(expected)) {
			t.Errorf("queued%d Go%s Source%s", i, actual, expected)
		}
	}
}
