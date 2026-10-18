package attachments_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestBuddyActualSourceIntroRawNameDedupMuteObserverAndOriginalRenderer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Global  protocol.JSONObject   `json:"global"`
		History []protocol.JSONObject `json:"history"`
	}
	rows := []row{}
	for _, input := range []string{`{}`, `{"companion":{"name":"Old pet","hatchedAt":0}}`, `{"companion":{"name":"\ud800","hatchedAt":0}}`, `{"companion":{"name":"\ud801","hatchedAt":0},"companionMuted":true}`, `{"companion":{"name":"\ud801","hatchedAt":0}}`} {
		var global protocol.JSONObject
		_ = global.UnmarshalJSON([]byte(input))
		for _, raw := range []string{`[]`, `[{"type":"attachment","attachment":{"type":"companion_intro","name":"Old pet"}}]`, `[{"type":"attachment","attachment":{"type":"companion_intro","name":"\ud800"}}]`, `[{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"function debug error"}]},"uuid":"actual-assistant","timestamp":"actual"}]`} {
			var history []protocol.JSONObject
			_ = json.Unmarshal([]byte(raw), &history)
			rows = append(rows, row{global, history})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-intro.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual intro Source %v %.5000s", err, output)
	}
	var expected []struct {
		Attachments   []protocol.JSONObject
		Rendered      []protocol.JSONObject
		Reactions     []string
		Before, After string
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		value, err := buddy.IntroAttachment(row.Global, row.History)
		if err != nil {
			t.Fatal(err)
		}
		if expected[i].Before != expected[i].After || len(expected[i].Reactions) != 0 {
			t.Fatal("Actual REPL envelopes must stay immutable and produce no role-only observer quip", i, expected[i])
		}
		if value == nil {
			if len(expected[i].Attachments) != 0 {
				t.Fatal("Source intro suppression", i)
			}
			continue
		}
		actual, _ := value.MarshalJSON()
		want, _ := expected[i].Attachments[0].MarshalJSON()
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
			t.Fatalf("Raw intro%d got%s Source%s", i, actual, want)
		}
		factory, err := messages.NewFactory(func() string { return "buddy-attachment" }, func() time.Time { return time.UnixMilli(1700000000000) })
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := attachments.Project(actual, factory)
		if err != nil || len(rendered) != 1 || len(expected[i].Rendered) != 1 {
			t.Fatal(i, err, rendered)
		}
		envelope, _ := rendered[0].Object("message")
		if string(envelope.Get("content")) != string(expected[i].Rendered[0].Get("content")) || string(rendered[0].Get("isMeta")) != "true" {
			t.Fatal("Actual intro renderer", i, string(envelope.Get("content")), string(expected[i].Rendered[0].Get("content")))
		}
	}
}
