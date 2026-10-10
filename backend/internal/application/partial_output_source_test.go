package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPartialOutputTrackerMatchesActualSourceResponseAndTurnTransitions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	assistant := func(id, text, parent string, known bool) protocol.JSONObject {
		out := obj(raw(map[string]any{"type": "assistant", "message": map[string]any{"id": id, "content": []any{map[string]any{"type": "text", "text": text}}}, "parent_tool_use_id": nil}))
		if parent != "" {
			out.Set("parent_tool_use_id", raw(parent))
		}
		if known {
			out.Set("error", raw("unknown"))
		}
		return out
	}
	type row struct {
		Messages []protocol.JSONObject `json:"messages"`
		Result   string                `json:"result"`
		Error    bool                  `json:"error"`
	}
	rows := []row{}
	for _, result := range []string{"API Error: final", "final", "final\n"} {
		for _, messages := range [][]protocol.JSONObject{
			{assistant("a", "first", "", false), assistant("a", " second", "", false), assistant("e", result, "", true)},
			{assistant("a", "same", "", false), assistant("e", result, "", false)},
			{assistant("a", result, "", false), assistant("e", result, "", true)},
			{assistant("a", "API Error: first", "", true), assistant("e", result, "", true)},
			{assistant("a", "stale", "", false), assistant("b", "current😀", "", false), assistant("e", result, "", true)},
			{assistant("a", "nested", "parent", false), assistant("e", result, "", true)},
			{assistant("a", "stale", "", false), obj([]byte(`{"type":"user","parent_tool_use_id":null}`)), assistant("e", result, "", true)},
			{assistant("a", "stale", "", false), obj([]byte(`{"type":"system","subtype":"compact_boundary"}`)), assistant("e", result, "", true)},
			{assistant("a", "retained", "", false), obj([]byte(`{"type":"user","parent_tool_use_id":"nested"}`)), assistant("e", result, "", true)},
		} {
			rows = append(rows, row{messages, result, true}, row{messages, result, false})
		}
	}
	home := t.TempDir()
	config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
	for _, p := range []string{config, temp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-partial-output.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PARTIAL_OUTPUT_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.2400s", e, stderr.String())
	}
	var source struct {
		Rows []struct{ Result, Line string }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source shape %v %.2400s", e, data)
	}
	for n, item := range rows {
		var tracker printPartialOutputTracker
		for _, m := range item.Messages {
			tracker.observe(m)
		}
		if tracker.formatResult(item.Result, item.Error) != source.Rows[n].Result || tracker.formatResultLine(item.Result, item.Error) != source.Rows[n].Line {
			t.Fatalf("Source response/turn transition differed at %d", n)
		}
	}
}
