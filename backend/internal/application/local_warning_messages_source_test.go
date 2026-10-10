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
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestOriginalInkUserPromptWarningMatchesActualSessionLocalPresentation(t *testing.T) {
	var record protocol.JSONObject
	if err := record.UnmarshalJSON([]byte(`{"type":"system","subtype":"informational","content":"UserPromptSubmit operation blocked by hook:\nphysical warning\n\nOriginal prompt: original user","isMeta":false,"timestamp":"2023-11-14T22:13:20.000Z","uuid":"original-warning","level":"warning"}`)); err != nil {
		t.Fatal(err)
	}
	var events []tui.Event
	s := &Session{turnID: "actual", emit: func(event tui.Event) error { events = append(events, event); return nil }}
	if err := s.localInputMessages([]protocol.JSONObject{record}); err != nil || len(events) != 1 || events[0].Kind != tui.LocalProcessedMessages || events[0].TurnID != "actual" || len(events[0].LocalMessages) != 1 || events[0].LocalMessages[0].Kind != "system" {
		t.Fatal("actual warning consumed by user-only callback", err, events)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../tui/testdata/local_warnings.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_WARNINGS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(map[string]any{"home": home, "records": []protocol.JSONObject{record}})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original warning renderer %v %.4000s", err, out)
	}
	var source struct {
		Visible []struct {
			Content, Color string
			Dot, DimColor  bool
		}
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Visible) != 1 {
		t.Fatal(err, string(out))
	}
	actual := events[0].LocalMessages[0]
	if source.Visible[0].Content != actual.Text || source.Visible[0].Color != actual.Level || !source.Visible[0].Dot || source.Visible[0].DimColor {
		t.Fatal("actual informational warning differs from original child props", actual, string(out))
	}
}
