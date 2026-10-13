package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceRunAgentOnlyDefinedForkContextClonesParentCache(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../testdata/native-agent-file-cache.fixture.ts")
	preload, _ := filepath.Abs("../../../preload.ts")
	payload, _ := json.Marshal(map[string]string{"home": home})
	cmd := exec.Command(bun, "--no-env-file", "--preload", preload, fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_AGENT_FILE_CACHE_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source: %v %.3000s", err, out)
	}
	var receipt struct {
		Rows []struct {
			ForkContextDefined, SameCacheObject bool
			Validation                          struct {
				Result    bool
				ErrorCode int
			}
			Bytes, ParentContent string
		}
	}
	if err := json.Unmarshal(out, &receipt); err != nil || len(receipt.Rows) != 2 {
		t.Fatalf("Source shape %v %.3000s", err, out)
	}
	first, next := receipt.Rows[0], receipt.Rows[1]
	if first.ForkContextDefined || first.SameCacheObject || first.Validation.Result || first.Bytes != "PARENT_READ_BEFORE\n" || !next.ForkContextDefined || next.SameCacheObject || !next.Validation.Result || next.Bytes != "SECOND_PARENT_CACHE_WRITE_OK\n" || next.ParentContent != "PARENT_READ_BEFORE\n" {
		t.Fatalf("actual Source clone semantics: %s", out)
	}
}
