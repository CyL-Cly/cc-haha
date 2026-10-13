package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourcePowerShellAndShellPermissionSuggestionLabels(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	profiles := []string{`[]`, `[{"type":"setMode","mode":"acceptEdits","destination":"session"}]`, `[{"type":"addRules","rules":[{"toolName":"SHELL","ruleContent":"command:*"}]}]`, `[{"type":"addRules","rules":[{"toolName":"SHELL","ruleContent":"command:*"},{"toolName":"SHELL","ruleContent":"command:*"},{"toolName":"SHELL","ruleContent":"other"}]}]`, `[{"type":"addRules","rules":[{"toolName":"Read","ruleContent":"/root/read/**"}]}]`, `[{"type":"addDirectories","directories":["/root/one"]}]`, `[{"type":"addDirectories","directories":["/root/one","/root/two","/root/three","/root/four"]}]`, `[{"type":"addDirectories","directories":["/root/one"]},{"type":"addRules","rules":[{"toolName":"Read","ruleContent":"/root/read/**"}]}]`, `[{"type":"addDirectories","directories":["/root/one"]},{"type":"addRules","rules":[{"toolName":"SHELL","ruleContent":"command:*"}]}]`, `[{"type":"addDirectories","directories":["/root/one","/root/two"]},{"type":"addRules","rules":[{"toolName":"SHELL","ruleContent":"command:*"},{"toolName":"SHELL","ruleContent":"other:*"},{"toolName":"SHELL","ruleContent":"third:*"}]}]`, `[{"type":"addRules","rules":[{"toolName":"SHELL","ruleContent":"` + strings.Repeat("😀", 26) + `"}]}]`, `[{"type":"addDirectories","directories":["/"]}]`}
	rows := []map[string]any{}
	for _, shell := range []string{"PowerShell", "Bash"} {
		for _, profile := range profiles {
			var suggestions []protocol.JSONObject
			_ = json.Unmarshal([]byte(strings.ReplaceAll(profile, "SHELL", shell)), &suggestions)
			rows = append(rows, map[string]any{"shell": shell, "suggestions": suggestions})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "cwd": work, "shellLabelRows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct{ ShellLabelRows []string }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if len(source.ShellLabelRows) != len(rows) {
		t.Fatal("Source shell UI labels receipt incomplete")
	}
	for i, row := range rows {
		got := terminalShellSuggestionLabel(row["suggestions"].([]protocol.JSONObject), row["shell"].(string), work, "darwin")
		if got != source.ShellLabelRows[i] {
			t.Errorf("Source label row%d native=%q Source=%q", i, got, source.ShellLabelRows[i])
		}
	}
}
func TestEditedPowerShellPermissionUpdatesAreRequestBoundAndEmptyMeansAllowOnce(t *testing.T) {
	request := tui.PermissionRequest{ToolName: "PowerShell", Choices: []tui.PermissionChoice{{Editable: true}}}
	prefix := "\uFEFF touch:* \u00a0"
	decision := tui.PermissionDecision{Allowed: true, EditedPrefix: &prefix}
	if err := terminalEditedPermissionUpdates(request, &decision); err != nil {
		t.Fatal(err)
	}
	expected := `[{"type":"addRules","rules":[{"toolName":"PowerShell","ruleContent":"touch:*"}],"behavior":"allow","destination":"localSettings"}]`
	got, _ := protocol.ParseAndStringifyJavaScriptJSON(decision.PermissionUpdates)
	want, _ := protocol.ParseAndStringifyJavaScriptJSON([]byte(expected))
	if string(got) != string(want) {
		t.Fatal("edited source update", string(got), string(want))
	}
	prefix = " \uFEFF"
	if err := terminalEditedPermissionUpdates(request, &decision); err != nil || string(decision.PermissionUpdates) != "[]" {
		t.Fatal("empty edit must allow only this call", err, string(decision.PermissionUpdates))
	}
	for _, request := range []tui.PermissionRequest{{ToolName: "Bash", Choices: []tui.PermissionChoice{{Editable: true}}}, {ToolName: "PowerShell"}} {
		if err := terminalEditedPermissionUpdates(request, &decision); err == nil {
			t.Fatal("unowned edited rule accepted", request)
		}
	}
}
