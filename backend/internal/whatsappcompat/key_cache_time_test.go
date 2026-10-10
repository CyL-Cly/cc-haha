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

func TestActualOriginalKeyCacheTTLAndWriteFailureRetainsCachedValue(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_cache_time.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
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
	now := time.UnixMilli(1000000)
	reads := 0
	stored := any(map[string]any{"value": 1})
	fail := false
	cache := NodeSignalKeyCache{Now: func() time.Time { return now }, Read: func(context.Context, string, []string) (map[string]any, error) {
		reads++
		return map[string]any{"key": stored}, nil
	}, Write: func(_ context.Context, v map[string]map[string]any) error {
		if fail {
			return errors.New("private physical write failure")
		}
		stored = v["session"]["key"]
		return nil
	}}
	snapshots := []any{}
	capture := func() {
		value, e := cache.Get(ctx, "session", []string{"key"})
		if e != nil {
			t.Fatal(e)
		}
		snapshots = append(snapshots, value)
	}
	capture()
	stored = map[string]any{"value": 2}
	now = now.Add(299999 * time.Millisecond)
	capture()
	now = now.Add(2 * time.Millisecond)
	capture()
	fail = true
	e = cache.Set(ctx, map[string]map[string]any{"session": {"key": map[string]any{"value": 3}}})
	if e == nil {
		t.Fatal("write failure missing")
	}
	failure := e.Error()
	capture()
	got := map[string]any{"snapshots": snapshots, "reads": reads, "error": failure}
	if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
		t.Fatal("original cache expiry/write-error behavior mismatch")
	}
}
