package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHookPathSafetyUsesActualSourceThreePassesAndNativeSymlinkForms(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	_ = os.MkdirAll(filepath.Join(cwd, ".git"), 0700)
	_ = os.WriteFile(filepath.Join(cwd, ".git", "config"), []byte("private fixture"), 0600)
	if e := os.Symlink(filepath.Join(cwd, ".git", "config"), filepath.Join(cwd, "alias")); e != nil {
		t.Fatal(e)
	}
	type row struct {
		Path          string   `json:"path"`
		Forms         []string `json:"forms"`
		CWD           string   `json:"cwd"`
		Platform      string   `json:"platform"`
		SettingsPaths []string `json:"settingsPaths"`
		AllowUNC      bool     `json:"allowUNC"`
	}
	rows := []row{}
	for _, platform := range []string{"darwin", "linux", "windows", "wsl"} {
		for _, name := range []string{"ordinary.txt", ".cLAuDE/settings.json", "other/.claude/settings.local.json", ".claude/agents/private.md", ".claude/worktrees/tree/ordinary.txt", ".claude/worktrees/tree/.claude/agents/private.md", ".git/config", ".vScOdE/settings.json", ".idea/a", ".BaShRc", "file:stream", "GIT~1/config", "file...", "file.PRN", ".../file", "file...txt", "//server/path", `\\server\path`, "https://example.com/path", "file \n", "override.json", "alias"} {
			path := filepath.Join(cwd, name)
			if name == "//server/path" || name == `\\server\path` || name == "https://example.com/path" {
				path = name
			}
			forms := []string{path}
			if canonical := resolved(path); canonical != path {
				forms = append(forms, canonical)
			}
			rows = append(rows, row{path, forms, cwd, platform, []string{filepath.Join(cwd, "override.json")}, false})
		}
	}
	rows = append(rows, row{Path: "//server/path", Forms: []string{"//server/path"}, CWD: cwd, Platform: "windows", AllowUNC: true}, row{Path: filepath.Join(cwd, "GreekΟΣ", ".claude", "skills", "a.md"), Forms: []string{filepath.Join(cwd, "GreekΟΣ", ".claude", "skills", "a.md")}, CWD: filepath.Join(cwd, "Greekος"), Platform: "darwin"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	for i := range rows {
		if rows[i].SettingsPaths == nil {
			rows[i].SettingsPaths = []string{}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "pathSafety": rows})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, r := range rows {
		got := CheckHookPathSafety(r.Path, r.Forms, HookPathSafetyContext{CWD: r.CWD, Platform: r.Platform, SettingsPaths: r.SettingsPaths}, r.AllowUNC)
		actual, _ := json.Marshal(got)
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i])) {
			t.Errorf("row%d Go%s Source%s", i, actual, source.Rows[i])
		}
	}
}
