package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func actualSource(t *testing.T, name string, input map[string]any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	input["homeDir"] = home
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-scheduler-" + name + ".fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	flag := "CC_HAHA_SCHEDULER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), flag + "_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
	payload, _ := json.Marshal(input)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %s %v %.4000s", name, e, stderr.String())
	}
	var result struct{ Capabilities struct{ Blocked []string } }
	if json.Unmarshal(out, &result) != nil || len(result.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped%v %.500s", result.Capabilities.Blocked, out)
	}
	return out
}
