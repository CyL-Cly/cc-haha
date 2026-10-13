package builtintools

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInternalReadPortableSafetyAgainstOriginalWindowsWSLAndUnixPredicate(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Path     string `json:"path"`
		Platform string `json:"platform"`
		AllowUNC bool   `json:"allowUNC"`
	}
	rows := []row{}
	for _, platform := range []string{"darwin", "linux", "windows", "wsl"} {
		for _, allow := range []bool{false, true} {
			for _, path := range []string{"/private/tasks/file", "/private/tasks/GIT~1/x", "/private/tasks/file.", "/private/tasks/file ", "/private/tasks/file.CON", "/private/tasks/.../x", "/private/tasks/a:b", `C:\task\a:b`, `\\?\C:\task\x`, `//?/C:/task/x`, `\\server\share\file`, `//server/share/file`, `/folder/\\server/file`, "/folder/DavWWWRoot/x", "https://host/path"} {
				rows = append(rows, row{path, platform, allow})
			}
		}
	}
	payload, _ := json.Marshal(rows)
	sourceFixture, _ := filepath.Abs("../../testdata/native-read-path-safety.fixture.ts")
	home := t.TempDir()
	cmd := exec.Command(bun, "--no-env-file", sourceFixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_READ_SAFETY_FIXTURE=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.1500s", e, out)
	}
	var expected []bool
	if e = json.Unmarshal(out, &expected); e != nil || len(expected) != len(rows) {
		t.Fatal("Source shape", e, string(out))
	}
	for i, row := range rows {
		if got := suspiciousHookPath(row.Path, row.Platform, row.AllowUNC); got != expected[i] {
			t.Errorf("row%d %+v Go%v Source%v", i, row, got, expected[i])
		}
	}
	s, _, _, _ := fixture(t)
	s.options.Platform = "windows"
	if _, e = New(s.options); e != nil {
		t.Fatal("closed Windows native filesystem/shell bindings rejected", e)
	}
}
