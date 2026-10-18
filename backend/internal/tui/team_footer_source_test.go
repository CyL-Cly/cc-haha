package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPaneTeamActualSourceRosterPrimaryCountAndPRVisibility(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	for _, teams := range []bool{false, true} {
		for _, inProcess := range []bool{false, true} {
			for _, roster := range []map[string]any{nil, {}, {"lead": map[string]string{"name": "team-lead"}}, {"lead": map[string]string{"name": "team-lead"}, "worker": map[string]string{"name": "worker"}}} {
				for _, activeMode := range []bool{false, true} {
					for _, background := range []bool{false, true} {
						for _, width := range []int{79, 80} {
							row := map[string]any{"teams": teams, "inProcess": inProcess, "activeMode": activeMode, "background": background, "prEnabled": true, "pr": map[string]any{"number": 572, "reviewState": "approved", "url": "https://example.invalid/pr/572"}, "columns": width}
							if roster != nil {
								row["context"] = map[string]any{"teammates": roster}
							}
							rows = append(rows, row)
						}
					}
				}
			}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-team-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAM_FOOTER_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "modes": []any{}, "footers": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var original struct {
		Footers []struct {
			HasTeams, ShouldShowPrStatus bool
			PrimaryItemCount             int
		}
	}
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err, string(output))
	}
	if len(original.Footers) != len(rows) {
		t.Fatal(string(output))
	}
	for i, row := range rows {
		s := NewState(nil, row["columns"].(int), 24)
		s.Preferences.PRStatusFooterEnabled = true
		s.Apply(Event{Kind: TerminalPRStatusChanged, Result: json.RawMessage(`{"number":572,"reviewState":"approved","url":"https://example.invalid/pr/572"}`)})
		s.paneTeam = original.Footers[i].HasTeams
		if row["activeMode"].(bool) {
			s.PermissionMode = "plan"
		}
		if row["background"].(bool) {
			s.BackgroundTasks = presentationObject(json.RawMessage(`{"actual":{"status":"running","isBackgrounded":true}}`))
		}
		if (len(s.terminalPRFooterLines()) > 0) != original.Footers[i].ShouldShowPrStatus {
			t.Fatalf("profile %d actual primary consumers=%v Source=%+v", i, row, original.Footers[i])
		}
	}
}
