package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceOutsidePreAbortedParentIsSynchronousAndCannotSpawn(t *testing.T) {
	fixture, _ := filepath.Abs("testdata/outside-pre-aborted.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	bytes, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(bytes))
	}
	var original struct {
		ImmediatelyAborted bool
		Matched, Spawned   int
		Result             []json.RawMessage
	}
	if e = json.Unmarshal(bytes, &original); e != nil {
		t.Fatal(e, string(bytes))
	}
	if !original.ImmediatelyAborted || original.Matched != 1 || original.Spawned != 0 || len(original.Result) != 0 {
		t.Fatal("unexpected Source pre-abort", string(bytes))
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 1000; i++ {
		child, cleanup := outsideCommandContext(parent, time.Minute)
		if child.Err() == nil {
			cleanup()
			t.Fatalf("construction admitted pre-aborted native child at %d", i)
		}
		cleanup()
	}
	child, cleanup := outsideCommandContext(parent, time.Minute)
	defer cleanup()
	marker := filepath.Join(t.TempDir(), "must-not-spawn")
	hook := protocol.NewJSONObject()
	if e = hook.UnmarshalJSON([]byte(`{"type":"command","command":"printf forbidden > ` + marker + `"}`)); e != nil {
		t.Fatal(e)
	}
	out, e := RunCommandProcess(child, CommandProcessRequest{Hook: hook, CWD: filepath.Dir(marker), OriginalCWD: filepath.Dir(marker), ProjectRoot: filepath.Dir(marker), Platform: "darwin", Environment: map[string]string{"HOME": filepath.Dir(marker), "PATH": "/usr/bin:/bin"}, NowMillis: func() float64 { return float64(time.Now().UnixMilli()) }})
	if e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if !out.Aborted {
		t.Fatal("native pre-abort lost", out)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("pre-aborted physical command ran", e)
	}
}
