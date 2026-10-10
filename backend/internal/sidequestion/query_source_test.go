package sidequestion

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Executes original query()+runForkedAgent(), not a pre-rendered fixture or
// substituted query. Only its upstream model is a real loopback HTTP server.
func TestActualSourceForkCanCompleteMainWhileSideSocketHeld(t *testing.T) {
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/side-question-query.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "ANTHROPIC_API_KEY=fixture", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=0", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX=0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("original query/fork", e, stderr.String())
	}
	var actual []map[string]any
	if e = json.Unmarshal(out, &actual); e != nil {
		t.Fatal(e, string(out), stderr.String())
	}
	expected := []map[string]any{}
	for _, order := range []string{"main-first", "side-first"} {
		expected = append(expected, map[string]any{"order": order, "mainCompletedBeforeSide": true, "response": "side answer", "parentUnchanged": true, "mainAborted": false, "calls": float64(2), "executions": float64(0)})
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Source real fork %s", out)
	}
}
