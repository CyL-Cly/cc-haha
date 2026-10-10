package taskstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This explicit developer seam runs the actual pinned Source lock wrapper.
// It is separate from dependency-free Go runtime tests and never locates a
// credential, launches a provider, or accesses a real user's config.
func TestActualSourceLeaseExcludesNativeTaskWriter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	for _, dir := range []string{"tmp", ".claude"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0777); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-task-lock.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_TASK_LOCK_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || line != "SOURCE_LOCKED\n" {
		t.Fatalf("Source did not acquire lease %q %v", line, err)
	}
	service := testStore(t, filepath.Join(home, ".claude"))
	complete := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); _, err := service.Create("interop", baseTask("native")); complete <- err }()
	<-started
	select {
	case err := <-complete:
		t.Fatalf("native writer crossed held Source lease: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "tasks", "interop", "1.json")); !os.IsNotExist(err) {
		t.Fatalf("native task written under held Source lease: %v", err)
	}
	if _, err := fmt.Fprintln(input, "release"); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Released     bool
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal([]byte(line), &receipt); err != nil || !receipt.Released || len(receipt.Capabilities.Blocked) > 0 {
		t.Fatalf("Source fixture guard receipt %s %v", line, err)
	}
	if err := <-complete; err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("Source lock process %v %s", err, stderr.String())
	}
	snapshot, err := service.Snapshot("interop")
	if err != nil || len(snapshot.Tasks) != 1 || snapshot.Revision != 1 {
		t.Fatalf("native transaction after Source release %#v %v", snapshot, err)
	}
	assertNoLeases(t, home)
}
