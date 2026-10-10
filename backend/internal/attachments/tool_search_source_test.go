package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceDeferredToolsDeltaAttachmentProjection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	inputs := []string{`{"type":"deferred_tools_delta","addedNames":[],"addedLines":[],"removedNames":[]}`, `{"type":"deferred_tools_delta","addedNames":["Read"],"addedLines":["Read","mcp__\ud800__𐐀"],"removedNames":[]}`, `{"type":"deferred_tools_delta","addedNames":[],"addedLines":[],"removedNames":["old","gone"]}`, `{"type":"deferred_tools_delta","addedNames":["Write"],"addedLines":["Write"],"removedNames":["disconnected"]}`, `{"type":"deferred_tools_delta","addedNames":[],"addedLines":[null,0,false,[1,2],{"x":true}],"removedNames":[]}`}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-search.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_SEARCH_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "projections": inputs})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Projections []struct {
			Messages    []protocol.JSONObject
			Allocations int
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Projections) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, len(source.Projections), source.Capabilities.Blocked)
	}
	allocations := 0
	factory, _ := messages.NewFactory(func() string { allocations++; return "tool-search-delta-" + strconv.Itoa(allocations) }, func() time.Time { return time.UnixMilli(1700000000000) })
	for i, input := range inputs {
		actual, e := attachments.Project(json.RawMessage(input), factory)
		if e != nil {
			t.Fatal(i, e)
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(source.Projections[i].Messages)
		if js(a) != js(b) || allocations != source.Projections[i].Allocations {
			t.Errorf("projection%d Go=%s/%d Source=%s/%d", i, a, allocations, b, source.Projections[i].Allocations)
		}
	}
}
