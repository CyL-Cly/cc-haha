package sdkhost

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

// This is the unchanged original Source's race/lifetime receipt. The paired
// native consumer must prove the same transitions before its guard is removed.
func TestActualSourceSDKPermissionRaceRetainsLateHookSideEffectsAndCancelsOnlySDK(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, ".git", "HEAD"), []byte("ref: refs/heads/private-race\n"), 0600); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{{"name": "hook-first", "winner": "hook"}, {"name": "sdk-first-late-allow", "winner": "sdk", "lateUpdates": true}, {"name": "sdk-deny-late-allow", "winner": "sdk", "deny": true, "lateUpdates": true}, {"name": "hook-passthrough", "winner": "passthrough"}, {"name": "actual-read-completed-late-update", "winner": "sdk", "lateUpdates": true, "physicalTool": true}, {"name": "actual-parent-abort", "winner": "cancel"}}
	ctx, finish := context.WithTimeout(context.Background(), 15*time.Second)
	defer finish()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-permission-hook-race.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "ANTHROPIC_API_KEY=fake-source-no-network", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_PERMISSION_RACE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	c.Stdin = bytes.NewReader(raw(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	data, e := c.Output()
	if e != nil {
		t.Fatal("actual original SDK permission race", e, stderr.String())
	}
	var source struct {
		Rows           []protocol.JSONObject
		ChildToolNames []string
		Capabilities   protocol.JSONObject
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(data))
	}
	if len(source.ChildToolNames) != 1 || source.ChildToolNames[0] != "Read" {
		t.Fatal("Original child ToolUseContext catalog", source.ChildToolNames)
	}
	var blocked []string
	_ = json.Unmarshal(source.Capabilities.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal("Source escaped fixture", blocked)
	}
	for i, row := range source.Rows {
		requests := []protocol.JSONObject{}
		_ = json.Unmarshal(row.Get("requests"), &requests)
		if len(requests) != 2 || i != 5 && string(row.Get("parentAborted")) != "false" {
			t.Fatal("Source race did not preserve parent and start both producers", i, string(raw(row)))
		}
		if i == 0 {
			cancel, _ := row.Object("cancel")
			if cancel.String("type") != "control_cancel_request" || row.String("permissionMode") != "default" {
				t.Fatal("Source hook win cancel", string(raw(row)))
			}
		}
		if i == 1 || i == 2 || i == 4 {
			if row.String("permissionMode") != "acceptEdits" {
				t.Fatal("Source discarded late loser side effects", string(raw(row)))
			}
		}
		decision, _ := row.Object("decision")
		want := "allow"
		if i == 2 || i == 5 {
			want = "deny"
		}
		if i == 5 {
			var cancels []protocol.JSONObject
			_ = json.Unmarshal(row.Get("cancels"), &cancels)
			if len(cancels) != 2 || string(row.Get("parentAborted")) != "true" {
				t.Fatal("Original parent abort did not cancel both original requests", string(raw(row)))
			}
		}
		if i == 4 && (string(row.Get("physicalMarker")) != "true" || string(row.Get("toolSignalAborted")) != "false") {
			t.Fatal("Original real Read normal completion canceled late hook", string(raw(row)))
		}
		if decision.String("behavior") != want {
			t.Fatal("Source winner verdict", i, string(raw(row)))
		}
	}
}
