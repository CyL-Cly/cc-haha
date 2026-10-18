package clibootstrap

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This helper only uses existing stdlib/Source fixture boundaries. It also
// compiles against exact-old production for the cold constructor counterexample.
func managedSourceJSON(t *testing.T, env map[string]string, input map[string]any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun is required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(home, "tmp")
	if err = os.Mkdir(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-managed-policy.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(rawMain(input))
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "PATH=/usr/bin:/bin", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_MANAGED_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=0", "CLAUDE_CODE_OAUTH_TOKEN=fake-owned-env"}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source remote settings: %v\n%s\n%s", err, out, stderr.Bytes())
	}
	return out
}
