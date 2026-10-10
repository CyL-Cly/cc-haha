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

func TestActualOriginalQueuedPairSaveFailureOnlyWarns(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_pair_save_queue.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want map[string]any
	if e = json.Unmarshal(out, &want); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	device, n, e := OpenNodeDevice(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(dir, "creds.json"), 0700); e != nil {
		t.Fatal(e)
	}
	warnings := 0
	n.Warn = func(error) { warnings++ }
	e = n.PutDevice(ctx, device)
	returned := e == nil
	if e = n.WaitCredsSave(ctx); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(dir, "creds.json"))
	if e != nil {
		t.Fatal(e)
	}
	_, queued := nodeCredsSaveQueues.Load(dir)
	got := map[string]any{"returned": returned, "warnings": warnings, "queueCleared": !queued, "fileStillDirectory": info.IsDir()}
	if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
		t.Fatal("actual Source queued save semantics differ")
	}
}
