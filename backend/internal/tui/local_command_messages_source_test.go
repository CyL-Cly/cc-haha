package tui

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalLocalUserActualSourceMountedCommandStdoutHiddenLiveAndResume(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Text   string `json:"text"`
		Width  int    `json:"width"`
		System bool   `json:"system,omitempty"`
	}
	rows := []row{}
	for _, width := range []int{100, 47, 24} {
		for _, text := range []string{"<command-name>/plan</command-name>\n<command-message>plan</command-message>\n<command-args>open</command-args>", "<command-message>canonical-long-command</command-message><command-args>this is a long actual argument 汉字😀 with words</command-args>", "<command-message>owned-skill</command-message><skill-format>true</skill-format><command-args>ignored</command-args>", "<local-command-stdout>Enabled plan mode. You can now explore and design an implementation approach.</local-command-stdout>", "<local-command-stdout>\n  汉字😀 visible output  \n</local-command-stdout>", "<local-command-stdout>same</local-command-stdout><local-command-stderr>same</local-command-stderr>", "<local-command-stdout></local-command-stdout>", "<local-command-stdout>  </local-command-stdout>", "<local-command-stdout future='owned'>actual attributes</local-command-stdout>", "<local-command-caveat>original hidden caveat</local-command-caveat>", "(no content)", "<tick>10</tick>"} {
			rows = append(rows, row{text, width, false})
		}
		for _, text := range []string{"You pet Public Buddy! ♥", "<local-command-stdout>Enabled plan mode</local-command-stdout>", "actual local system info", "<command-name>/model</command-name><command-message>model</command-message><command-args>help</command-args>", "<local-command-stdout>Run /model to open the model selection menu, or /model [modelName] to set the model.</local-command-stdout>"} {
			rows = append(rows, row{text, width, true})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-local-user-render.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_USER_RENDER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual local UserTextMessage Source %v %.5000s", err, output)
	}
	var expected []struct{ Text string }
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	normalize := func(value string) string {
		lines := strings.Split(ansi.Strip(value), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
	for i, row := range rows {
		state := NewState(nil, row.Width, 30)
		state.Editor.Set("/pending")
		state.HandleKey(Key{Name: "enter"}, time.Now())
		if row.System {
			state.Apply(Event{Kind: LocalProcessedMessages, TurnID: state.ActiveTurn, LocalMessages: []LocalProcessedMessage{{Kind: "system", Text: row.Text, Subtype: "local_command", Level: "info"}}})
		} else {
			state.Apply(Event{Kind: LocalUserMessages, TurnID: state.ActiveTurn, UserMessages: []string{row.Text}})
		}
		actual := normalize(strings.Join(state.transcriptLines(), "\n"))
		want := normalize(expected[i].Text)
		if actual != want {
			t.Fatalf("Actual mounted local UserTextMessage width%d profile%d\nGo:%q\nSource:%q", row.Width, i, actual, want)
		}
		if len(state.Entries) != 1 || state.Entries[0].Text != row.Text {
			t.Fatal("Rendering altered original canonical projection", i, state.Entries)
		}
		var record protocol.JSONObject
		if row.System {
			record = presentationObject([]byte(`{"type":"system","subtype":"local_command","uuid":"saved-local","level":"info"}`))
			record.Set("content", mustLocalMessageJSON(row.Text))
		} else {
			record = presentationObject([]byte(`{"type":"user","uuid":"saved-local"}`))
			record.Set("message", mustLocalMessageJSON(map[string]any{"role": "user", "content": row.Text}))
		}
		before, _ := record.MarshalJSON()
		resumed := NewState(nil, row.Width, 30)
		resumed.SetInitialHistory([]protocol.JSONObject{record}, false)
		if got := normalize(strings.Join(resumed.transcriptLines(), "\n")); got != want {
			t.Fatalf("Saved canonical resume differs from actual Source width%d profile%d\nGo:%q\nSource:%q", row.Width, i, got, want)
		}
		after, _ := record.MarshalJSON()
		if !bytes.Equal(before, after) {
			t.Fatal("Resume rendering changed saved records", i)
		}
	}
}

func mustLocalMessageJSON(value any) json.RawMessage {
	result, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return result
}
