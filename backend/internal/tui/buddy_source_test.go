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

func TestBuddyActualSourceWholeCardLayoutWideNarrowAndRealKeys(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Global protocol.JSONObject `json:"global"`
		Args   string              `json:"args"`
		Now    []float64           `json:"now"`
		Random []float64           `json:"random"`
		Width  int                 `json:"width"`
	}
	rows := []row{}
	for _, width := range []int{100, 47, 24} {
		for _, input := range []string{`{}`, `{"companion":{"name":"Legacy pet","personality":"Old soul","hatchedAt":1777777777777}}`} {
			var global protocol.JSONObject
			_ = global.UnmarshalJSON([]byte(input))
			rows = append(rows, row{global, "info", []float64{1777777777777}, []float64{.125}, width})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-card.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual Source Buddy layout %v %.5000s", err, output)
	}
	var expected struct {
		Rows []struct{ Frames []struct{ Output string } }
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	normalize := func(value string) string {
		lines := strings.Split(ansi.Strip(value), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
	for index, row := range rows {
		companion, err := buddy.Get(row.Global)
		if err != nil {
			t.Fatal(err)
		}
		value := protocol.NewJSONObject()
		if companion != nil {
			data, _ := companion.MarshalJSON()
			value.Set("companion", data)
		} else {
			preview, _, err := buddy.Roll(buddy.UserID(row.Global), true)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := preview.MarshalJSON()
			value.Set("preview", data)
		}
		raw, _ := value.MarshalJSON()
		actual := normalize(strings.Join(terminalBuddyCardLines(raw, row.Width, "dark"), "\n"))
		want := normalize(expected.Rows[index].Frames[0].Output)
		if actual != want {
			t.Fatalf("Actual whole Buddy width%d profile%d\nGo:\n%s\nSource:\n%s", row.Width, index, actual, want)
		}
	}
}
