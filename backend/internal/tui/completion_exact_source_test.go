package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceExactPrefixCommandEnterWinsOverLongerBuiltinPrompt(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-exact-command-completion.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_EXACT_COMPLETION_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36"}
	input, _ := json.Marshal(map[string]any{"home": home, "queries": []string{"/sta", "/status", "/statusl", "/statusline"}})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Original command suggestions/apply/submit", err, string(output))
	}
	var source struct {
		Results []struct {
			Query, Value string
			Submits      [][]any
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != 4 || len(source.Caps.Blocked) > 0 || len(source.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	for _, row := range source.Results {
		s := NewState([]Completion{{Name: "statusline", Kind: "prompt"}, {Name: "status", Kind: "local-jsx"}}, 100, 36)
		s.Paste(row.Query)
		actions := s.HandleKey(Key{Name: "enter"}, time.Now())
		if len(actions) != 1 || len(row.Submits) != 1 {
			t.Errorf("Source actual Enter dispatch %q Source%v Native%v", row.Query, row.Submits, actions)
			continue
		}
		value := actions[0].input.Text
		if actions[0].kind == actionInvoke {
			value = "/" + strings.TrimSuffix(strings.TrimPrefix(actions[0].name, "command:panel:"), "\x00")
		}
		if strings.TrimSpace(value) != strings.TrimSpace(row.Value) {
			t.Errorf("Exact/prefix Enter changed command: query%q Source%q Native%q", row.Query, row.Value, value)
		}
	}
}
