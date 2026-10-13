package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveThinkingAndRedactedTranscriptAgainstActualSourceComponents(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, transcript := range []bool{false, true} {
		for _, redacted := range []bool{false, true} {
			for _, last := range []bool{false, true} {
				rows = append(rows, map[string]any{"text": "PRIVATE_THINKING_CONTENT", "id": "first", "lastId": map[bool]string{true: "first", false: "second"}[last], "transcript": transcript, "redacted": redacted})
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-thinking-summaries.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_THINKING_SUMMARIES_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "uiRows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []string
	if err := json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		s := NewState(nil, 100, 30)
		s.ActiveTurn = "actual-turn"
		s.Apply(Event{TurnID: s.ActiveTurn, Kind: ThinkingDelta, Text: row["text"].(string), Redacted: row["redacted"].(bool)})
		if row["lastId"] == "second" {
			s.Apply(Event{TurnID: "next-turn", Kind: ThinkingDelta, Text: "SECOND_PRIVATE_THINKING"})
			// A subsequent actual turn has already been admitted by the TUI.
			s.ActiveTurn = "next-turn"
			s.Apply(Event{TurnID: s.ActiveTurn, Kind: ThinkingDelta, Text: "SECOND_PRIVATE_THINKING"})
		}
		s.Transcript = row["transcript"].(bool)
		view := s.View("", false).Content
		if row["redacted"].(bool) {
			if strings.Contains(view, "✻ Thinking…") != strings.Contains(expected[i], "✻ Thinking…") {
				t.Fatal(i, view, expected[i])
			}
		} else if strings.Contains(view, row["text"].(string)) != strings.Contains(expected[i], row["text"].(string)) {
			t.Fatal(i, view, expected[i])
		}
		if strings.Contains(view, "PRIVATE_ENCRYPTED_DATA") {
			t.Fatal("redacted data exposed", i)
		}
	}
}
