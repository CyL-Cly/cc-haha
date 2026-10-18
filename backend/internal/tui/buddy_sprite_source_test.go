package tui

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuddyActualSourceMountedSpriteIdleBlinkPetNarrowAndCleanup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Global  protocol.JSONObject `json:"global"`
		Width   int                 `json:"width"`
		Focused bool                `json:"focused"`
		PetAt   json.RawMessage     `json:"petAt,omitempty"`
	}
	rows := []row{}
	for _, input := range []string{`{"companion":{"name":"Legacy pet","hatchedAt":0,"personality":"Old soul"}}`, `{"companion":{"name":"Odd 😀 name","hatchedAt":0,"appearanceSeed":"actual-sprite-seed"}}`, `{"companion":{"name":"Muted","hatchedAt":0},"companionMuted":true}`, `{}`} {
		var global protocol.JSONObject
		_ = global.UnmarshalJSON([]byte(input))
		for _, width := range []int{100, 99} {
			for _, focused := range []bool{false, true} {
				for _, pet := range []json.RawMessage{nil, []byte("0"), []byte("1777777777777")} {
					rows = append(rows, row{global, width, focused, pet})
				}
			}
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-sprite.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual mounted Source sprite %v %.5000s", err, output)
	}
	var expected []struct {
		Frames []struct {
			Tick int
			Text string
		}
		Reserved int
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(output))
	}
	normalize := func(lines []string) string {
		for i := range lines {
			lines[i] = strings.TrimRight(ansi.Strip(lines[i]), " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
	for i, row := range rows {
		companion, err := buddy.Get(row.Global)
		if err != nil {
			t.Fatal(err)
		}
		value := protocol.NewJSONObject()
		if companion != nil {
			data, _ := companion.MarshalJSON()
			value.Set("companion", data)
		}
		value.Set("muted", row.Global.Get("companionMuted"))
		if len(row.PetAt) > 0 {
			value.Set("petAt", row.PetAt)
		}
		data, _ := value.MarshalJSON()
		state := NewState(nil, row.Width, 30)
		state.applyTerminalBuddy(data)
		state.buddy.focused = row.Focused
		for _, frame := range expected[i].Frames {
			state.buddy.tick = frame.Tick
			actual := normalize(state.terminalBuddySpriteLines())
			want := strings.Trim(frame.Text, "\n")
			if actual != want {
				t.Fatalf("Mounted Source sprite profile%d width%d focus%v pet%s tick%d\nGo:%q\nSource:%q", i, row.Width, row.Focused, row.PetAt, frame.Tick, actual, want)
			}
		}
		if state.buddyReservedColumns() != expected[i].Reserved {
			t.Fatal("Actual prompt reservation", i, state.buddyReservedColumns(), expected[i].Reserved)
		}
	}
}
