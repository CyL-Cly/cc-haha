package application

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestOriginalInkLocalUserVisibilityMatchesActualSessionCallback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	text := `[ {"type":"user","isMeta":true,"message":{"content":"<local-command-caveat>Hidden original caveat</local-command-caveat>"}}, {"type":"user","message":{"content":"Usage: /agent <agent> <prompt>"}}, {"type":"user","message":{"content":"/modelonly"}}, {"type":"user","message":{"content":"This skill can only be invoked by Claude, not directly by users. Ask Claude to use the \"modelonly\" skill for you."}}, {"type":"user","isMeta":true,"message":{"content":"Never display meta"}} ]`
	var records []protocol.JSONObject
	if err := json.Unmarshal([]byte(text), &records); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../tui/testdata/local_users.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_USERS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "records": records})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original Ink local user dispatch %v %.4000s", err, out)
	}
	var source struct{ Visible []string }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	var events []tui.Event
	s := &Session{turnID: "actual", emit: func(event tui.Event) error { events = append(events, event); return nil }}
	if err = s.localInputMessages(records); err != nil || len(events) != 1 || events[0].Kind != tui.LocalUserMessages || events[0].TurnID != "actual" || !reflect.DeepEqual(events[0].UserMessages, source.Visible) {
		t.Fatal("actual terminal local visibility differs", err, events, source.Visible)
	}
}
