package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

func TestActualSourceBuddySlashCompletionCanonicalSystemMessagePaintAndResume(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-caller.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_CALLER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=120", "LINES=42"}
	input, _ := json.Marshal(map[string]any{"home": home, "width": 120, "args": []string{"pet", "mute"}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original processSlashCommand/Buddy effect/normalize/full Message", err, string(output))
	}
	var source struct {
		Results []struct {
			Args, Frame   string
			MessageFrames []string
			Messages      []protocol.JSONObject
			Global        protocol.JSONObject
			NormalizedAPI json.RawMessage
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 2 || len(source.Caps.Blocked) > 0 || len(source.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	normalize := func(value string) string {
		lines := strings.Split(ansi.Strip(value), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
	for index, row := range source.Results {
		stdout := []string{"You pet Public Buddy! ♥", "Public Buddy is now muted."}[index]
		if row.Frame != "❯ /buddy "+row.Args+"\n  ⎿  "+stdout || len(row.Messages) != 2 || len(row.MessageFrames) != 2 {
			t.Fatal("Original full caller must retain two visible system completion messages", row.Args, row.Frame, row.Messages)
		}
		if string(row.Global.Get("futureBuddy")) != `{"keep":true}` || row.Args == "mute" && string(row.Global.Get("companionMuted")) != "true" {
			t.Fatal("Actual Buddy completion did not preserve/mutate the original global fields", row.Global)
		}
		before, _ := json.Marshal(row.Messages)
		local := make([]LocalProcessedMessage, 0, len(row.Messages))
		for messageIndex, record := range row.Messages {
			if record.String("type") != "system" || record.String("subtype") != "local_command" || record.String("level") != "info" {
				t.Fatal("Actual display:system canonical kind changed", row.Args, record)
			}
			local = append(local, LocalProcessedMessage{Kind: "system", Subtype: "local_command", Level: "info", Text: record.String("content")})
			live := NewState(nil, 120, 42)
			live.Apply(Event{Kind: LocalProcessedMessages, Background: true, TurnID: record.String("uuid"), LocalMessages: local[messageIndex:]})
			resumed := NewState(nil, 120, 42)
			resumed.SetInitialHistory([]protocol.JSONObject{record}, false)
			for _, state := range []*State{live, resumed} {
				if got := normalize(strings.Join(state.transcriptLines(), "\n")); got != normalize(row.MessageFrames[messageIndex]) {
					t.Fatalf("Original full Message paint differs after live/resume projection %s message%d\nSource:%q\nGo:%q", row.Args, messageIndex, row.MessageFrames[messageIndex], got)
				}
				if len(state.Entries) != 1 || state.Entries[0].Text != record.String("content") || state.Entries[0].Subtype != "local_command" {
					t.Fatal("Visible paint changed canonical system content", row.Args, state.Entries)
				}
			}
		}
		after, _ := json.Marshal(row.Messages)
		if !bytes.Equal(before, after) || bytes.Count(row.NormalizedAPI, []byte("<command-message>buddy</command-message>")) != 1 || bytes.Count(row.NormalizedAPI, []byte(stdout)) != 1 {
			t.Fatal("Render/resume changed canonical records or original next-request normalization", row.Args, string(row.NormalizedAPI))
		}
	}
}
