package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceColdReadHistoryFeedsRealFileWriteConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../testdata/native-cold-file-cache.fixture.ts")
	preload, _ := filepath.Abs("../../../preload.ts")
	payload, _ := json.Marshal(map[string]string{"home": home})
	cmd := exec.Command(bun, "--no-env-file", "--preload", preload, fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_COLD_FILE_CACHE_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source: %v %.3000s", err, out)
	}
	var receipt struct {
		Restored struct {
			Content        string
			ValidTimestamp bool
		}
		Validation                        struct{ Result bool }
		ResultType, Physical              string
		ActualReadCalls, ActualWriteCalls int
	}
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("Source shape %v %.3000s", err, out)
	}
	if receipt.Restored.Content != "before" || !receipt.Restored.ValidTimestamp || !receipt.Validation.Result || receipt.ResultType != "update" || receipt.Physical != "RESTORED_CACHE_WRITE_OK\n" || receipt.ActualReadCalls != 1 || receipt.ActualWriteCalls != 1 {
		t.Fatalf("Source did not execute real cold producer/consumer: %s", out)
	}
}
