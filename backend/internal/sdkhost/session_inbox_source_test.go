package sdkhost_test

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
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

// The producer is the original QueryEngine and the original physical Project
// writer. The fixture holds only its external await boundary, never resolves
// the Go consumer or substitutes a golden for Source's attachment policy.
func TestActualOriginalInboxConsumerDurabilityReplayAndExternalFiltering(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, dir := range []string{config, temp} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	profiles := []map[string]any{
		{"replay": false}, {"replay": true}, {"replay": true, "disabled": true},
		{"replay": true, "failure": "record-failure"}, {"replay": true, "failure": "flush-failure"}, {"replay": false, "idle": true},
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-inbox-consumer.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SDK_INBOX_CONSUMER_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	body, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": profiles})
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.4000s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			SourceUUID, CanonicalUUID, Failure string
			Before                             *struct {
				Pending       bool
				Receipts      int
				Duplicate     protocol.JSONObject
				CanonicalUUID string
			}
			FinalPending, PersistedDelivery bool
			Receipts, Replay                []protocol.JSONObject
			Duplicate                       protocol.JSONObject
			PersistedTypes                  []string
		}
		Capabilities struct{ Blocked []any }
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatalf("%v %.4000s", e, data)
	}
	if len(source.Rows) != len(profiles) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("unexpected Source profile or external primitive", string(data))
	}
	uuid := sessioninbox.MessageUUID(json.RawMessage(`"busy-durable-message"`))
	for i, row := range source.Rows {
		if row.SourceUUID != uuid || row.CanonicalUUID != uuid {
			t.Fatalf("profile%d stable canonical identity: %.4000s", i, data)
		}
		failed := i == 3 || i == 4
		if row.FinalPending != failed || len(row.Receipts) != map[bool]int{false: 1, true: 0}[failed] {
			t.Fatalf("profile%d acknowledged before successful flush: %.4000s", i, data)
		}
		if i != 2 && i != 3 {
			if row.Before == nil || !row.Before.Pending || row.Before.Receipts != 0 || row.Before.CanonicalUUID != uuid || row.Before.Duplicate.String("status") != "queued" || string(row.Before.Duplicate.Get("duplicate")) != "true" {
				t.Fatalf("profile%d lost pending delivery during actual flush: %.4000s", i, data)
			}
		}
		wantReplay := 0
		if i == 1 || i == 2 {
			wantReplay = 1
		}
		if len(row.Replay) != wantReplay {
			t.Fatalf("profile%d replay count: %.4000s", i, data)
		}
		for _, replay := range row.Replay {
			var message protocol.JSONObject
			_ = json.Unmarshal(replay.Get("message"), &message)
			if replay.String("uuid") != uuid || replay.String("timestamp") != "2026-10-08T00:00:00.000Z" || string(replay.Get("parent_tool_use_id")) != "null" || message.String("role") != "user" || message.String("content") != "Please acknowledge progress" {
				t.Fatal(replay)
			}
		}
		// This is a Source deficiency, not permission to widen external logs.
		// Actual recordTranscript filters the queued attachment. A new inbox
		// loaded from those real rows cannot deduplicate its absent delivery ID.
		wantColdStatus, wantColdDuplicate := "queued", "false"
		if i == 5 {
			wantColdStatus, wantColdDuplicate = "consumed", "true"
		}
		if row.PersistedDelivery != (i == 5) || row.Duplicate.String("status") != wantColdStatus || string(row.Duplicate.Get("duplicate")) != wantColdDuplicate {
			t.Fatalf("profile%d changed original cold external filtering: %.4000s", i, data)
		}
		if i < 2 && len(row.PersistedTypes) == 0 {
			t.Fatalf("profile%d did not execute actual physical Source writer", i)
		}
	}
}
