package whatsappcandidate

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

func TestActualOriginalBulkWriteFailureStillStartsAllPhysicalSiblings(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_bulk_partial.fixture.ts"))
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
	for trial := 0; trial < 30; trial++ {
		dir := t.TempDir()
		_, n, e := OpenNodeDevice(dir, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Mkdir(n.keyPath("session", "bad"), 0700); e != nil {
			t.Fatal(e)
		}
		e = n.writeKeys(ctx, map[string]map[string]any{"session": {"bad": map[string]any{"value": 1}, "good": map[string]any{"value": 2}}})
		values, readErr := n.readKeys(ctx, "session", []string{"good"})
		data := nodeJSON(values["good"])
		var good any
		if readErr == nil {
			_ = json.Unmarshal(data, &good)
		}
		got := map[string]any{"failed": e != nil, "good": good}
		if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
			t.Fatal("actual Source sibling physical write was lost", trial)
		}
	}
}
