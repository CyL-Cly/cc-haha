package teamworker

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

func TestActualSourceProcessShutdownApprovalHistoryIsSeparateFromPaneProtocolAndRequiresCurrentRequest(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	stamp := time.UnixMilli(2000).UTC().Format(time.RFC3339Nano)
	approval := `{"type":"shutdown_approved","requestId":"r","from":"worker-one","timestamp":"t","paneId":"","backendType":"process"}`
	message := func(from, timestamp, text string) map[string]string {
		return map[string]string{"from": from, "timestamp": timestamp, "text": text}
	}
	profiles := []map[string]any{
		{"name": "worker-one", "since": 1000, "messages": []any{message("worker-one", stamp, approval)}},
		{"name": "worker-one", "since": 3000, "messages": []any{message("worker-one", stamp, approval)}},
		{"name": "worker-one", "since": 1000, "messages": []any{message("different", stamp, approval)}},
		{"name": "worker-one", "since": 1000, "messages": []any{message("worker-one", "not-a-date", approval)}},
		{"name": "worker-one", "messages": []any{message("worker-one", stamp, approval)}},
		{"name": "worker-one", "since": 1000, "messages": []any{message("worker-one", stamp, "{broken")}},
		{"name": "worker-one", "since": 1000, "messages": []any{message("worker-one", stamp, `{"type":"shutdown_rejected"}`)}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-supervisor-helpers.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_SUPERVISOR_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "shutdownRows": profiles}))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source physical approval history", err, string(out))
	}
	var source struct {
		ShutdownRows []struct {
			Approved, ClearedRequest bool
			Parsed                   []json.RawMessage
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.ShutdownRows) != len(profiles) {
		t.Fatal("actual Source shutdown history receipts", err, string(out))
	}
	for i, profile := range profiles {
		f := newSupervisorFixture(t)
		f.runtime.mu.Lock()
		launch := f.runtime.launches[f.plan.String("planId")]
		f.runtime.mu.Unlock()
		worker := f.worker("member-one")
		launch.supervise.Lock()
		worker.shutdownRequestedAt = time.Time{}
		if since, ok := profile["since"].(int); ok {
			worker.shutdownRequestedAt = time.UnixMilli(int64(since))
		}
		for _, value := range profile["messages"].([]any) {
			msg := value.(map[string]string)
			if err := f.mailbox.Write("team-lead", "supervised", validationObject(msg)); err != nil {
				t.Fatal(err)
			}
			if string(source.ShutdownRows[i].Parsed[0]) != "null" {
				t.Fatal("process-specific completion must not weaken the trusted pane approval schema", msg)
			}
		}
		approved := f.runtime.approvedShutdown(ctx, launch, worker)
		cleared := worker.shutdownRequestedAt.IsZero()
		launch.supervise.Unlock()
		if approved != source.ShutdownRows[i].Approved || cleared != source.ShutdownRows[i].ClearedRequest {
			t.Fatalf("Source current request/history boundary row%d Go=(%v,%v) Source=(%v,%v)", i, approved, cleared, source.ShutdownRows[i].Approved, source.ShutdownRows[i].ClearedRequest)
		}
	}
}
