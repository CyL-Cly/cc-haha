package powershellpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActualSourcePowerShellGitMetadataAndPathNormalization(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "WorkΣ")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	values := []string{"", ".", "HEAD", "objects/private", "refs", "hooks/pre-commit", ".git", ".git/config", "project/hooks/x", "head/private", "GIT~1/CONFIG", "Git~12/HEAD", "git~x/HEAD", "file.git", ".gitignore", "../WorkΣ/hooks/private", "../WorkΣ/../WorkΣ/.git/config", filepath.Join(work, "HEAD"), filepath.Join(home, "other", "HEAD"), `'hooks\pre-commit'`, `".git\config"`, `-Path:hooks/pre-commit`, `–Path:'hooks/pre-commit'`, `/Path:.git/config`, "FileSystem::hooks/pre-commit", `Microsoft.PowerShell.Core\FileSystem::.git/config`, `a\b\c\FileSystem::refs/private`, `a\b\c\d\FileSystem::refs/private`, `Fileſyſtem::.git/config`, `C:hooks/private`, `C:\hooks\private`, "hooks . /private", ".git ... /config", ".. . /WorkΣ/HEAD", ".../.git/config", ".git/config...", "h`ooks/pre-commit", "././refs//private", "/.git/config", "hooks//x/../private", "./hoOKS/private", "'HEAD'", "–Path:HEAD", "―Path:.git/config"}
	var source struct {
		GitRows []struct {
			Normalized       string
			Internal, DotGit bool
		}
	}
	sourcePolicy(t, map[string]any{"home": home, "cwd": work, "gitRows": values}, &source)
	if len(source.GitRows) != len(values) {
		t.Fatal("incomplete Source Git corpus")
	}
	c := PathContext{CWD: work, Home: home, Platform: "darwin"}
	for i, value := range values {
		got := normalizeGitArgument(value)
		if got != source.GitRows[i].Normalized || c.gitPath(value, false) != source.GitRows[i].Internal || c.gitPath(value, true) != source.GitRows[i].DotGit {
			t.Errorf("Source Git arg=%q native={normal:%q internal:%v dot:%v} Source=%+v", value, got, c.gitPath(value, false), c.gitPath(value, true), source.GitRows[i])
		}
	}
}
