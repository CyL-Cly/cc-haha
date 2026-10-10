package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualOriginalTransactionCommitRetryAndExhaustion(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_commit.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	failures := []int{0, 2, 10}
	cmd.Stdin = bytes.NewReader(nodeJSON(failures))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want any
	if e = json.Unmarshal(out, &want); e != nil {
		t.Fatal(e)
	}
	got := []any{}
	for _, count := range failures {
		_, n, e := OpenNodeDevice(t.TempDir(), nil)
		if e != nil {
			t.Fatal(e)
		}
		attempts := 0
		writes := []any{}
		n.WriteKeys = func(_ context.Context, v map[string]map[string]any) error {
			attempts++
			writes = append(writes, v)
			if attempts <= count {
				return errors.New("private commit failure")
			}
			return nil
		}
		waits := 0
		n.CommitSleep = func(_ context.Context, d time.Duration) error {
			if d != 3*time.Second {
				t.Fatal("original commit delay changed")
			}
			waits++
			return nil
		}
		e = n.commitSessionMutations(ctx, map[string]any{"111.0": map[string]any{"value": 1}})
		failure := ""
		if e != nil {
			failure = e.Error()
		}
		if waits != attempts-1 {
			t.Fatal("retry delays", waits, attempts)
		}
		got = append(got, map[string]any{"attempts": attempts, "error": failure, "writes": writes})
	}
	if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
		t.Fatal("Source original retry/commit boundary mismatch")
	}
}
