package application

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalPluginUpdateActualSourceCapturedCommandRemainsUntilExplicitActivation(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source and official MCPB reference required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	files, _, _ := prepareTerminalBulkFixture(t, work, filepath.Join(home, ".claude"))
	skill := filepath.Join(work, "market", "plugins", "zeta", "skills", "run", "SKILL.md")
	files[skill] = base64.StdEncoding.EncodeToString([]byte("---\ndescription: Actual original Skill\n---\nUPDATE_SKILL_ONE_734"))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-reload.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_RELOAD_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "entrypoint": "cli", "activation": true, "files": files}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual original activation %v %.5000s", err, output)
	}
	var source struct {
		First, BeforeReload, GlobalBeforeReload, AfterReload json.RawMessage
		Caps                                                 struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	if !strings.Contains(string(source.First), "UPDATE_SKILL_ONE_734") || !strings.Contains(string(source.BeforeReload), "UPDATE_SKILL_ONE_734") || strings.Contains(string(source.BeforeReload), "UPDATE_SKILL_TWO_734") || !strings.Contains(string(source.AfterReload), "UPDATE_SKILL_TWO_734") {
		t.Fatal("Original captured command activation boundary", string(output))
	}
	if !strings.Contains(string(source.GlobalBeforeReload), "UPDATE_SKILL_TWO_734") {
		t.Fatal("Original model Skill getCommands is a separate fresh-memo consumer", string(output))
	}
}
