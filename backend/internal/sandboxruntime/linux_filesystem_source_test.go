package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceLinuxFilesystemMountOrderingRealRipgrepSymlinksAndMissingPaths(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	temp := filepath.Join(home, "tmp")
	os.MkdirAll(cwd, 0700)
	os.MkdirAll(temp, 0700)
	for _, path := range []string{".git/hooks", "nested/.git/hooks", "nested/.vscode", "secret/allowed", "workspace/deep", "decoy"} {
		os.MkdirAll(filepath.Join(cwd, path), 0700)
	}
	for _, path := range []string{".git/config", "nested/.git/config", "nested/.git/hooks/hook", "nested/.vscode/settings.json", "secret/allowed/file", "workspace/deep/.bashrc", "real-file"} {
		os.WriteFile(filepath.Join(cwd, path), []byte("private source bytes"), 0600)
	}
	os.Symlink(filepath.Join(cwd, "decoy"), filepath.Join(cwd, "inside-link"))
	os.Symlink(home, filepath.Join(cwd, "outbound-link"))
	rg, e := exec.LookPath("rg")
	if e != nil {
		t.Fatal(e)
	}
	type row struct {
		Read           *ReadRestriction  `json:"read,omitempty"`
		Write          *WriteRestriction `json:"write,omitempty"`
		Depth          int               `json:"depth"`
		AllowGitConfig bool              `json:"allowGitConfig"`
	}
	rows := []row{
		{},
		{Write: &WriteRestriction{AllowOnly: []string{cwd, "/dev/null", filepath.Join(cwd, "absent"), filepath.Join(cwd, "outbound-link")}, DenyWithinAllow: []string{filepath.Join(cwd, "inside-link/settings.json"), filepath.Join(cwd, "real-file/under-file"), filepath.Join(cwd, "workspace/missing/deeper"), filepath.Join(cwd, "secret/allowed/file"), filepath.Join(cwd, "real-file"), filepath.Join(cwd, "real-file")}}, Depth: 3},
		{Read: &ReadRestriction{DenyOnly: []string{filepath.Join(cwd, "secret")}, AllowWithinDeny: []string{filepath.Join(cwd, "secret/allowed"), filepath.Join(cwd, "missing")}}, Write: &WriteRestriction{AllowOnly: []string{cwd}, DenyWithinAllow: []string{}}, Depth: 3},
		{Read: &ReadRestriction{DenyOnly: []string{cwd}, AllowWithinDeny: []string{filepath.Join(cwd, "secret/allowed")}}, Write: &WriteRestriction{AllowOnly: []string{filepath.Join(cwd, "workspace")}, DenyWithinAllow: []string{filepath.Join(cwd, "workspace/deep/.bashrc")}}, Depth: 2, AllowGitConfig: true},
		{Read: &ReadRestriction{DenyOnly: []string{filepath.Join(cwd, "real-file"), filepath.Join(cwd, "missing"), filepath.Join(cwd, "secret/allowed/file")}, AllowWithinDeny: []string{filepath.Join(cwd, "secret")}}},
	}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "tmp": temp, "rg": rg, "rows": rows})
	fixture, _ := filepath.Abs("testdata/linux-filesystem.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/opt/homebrew/bin:/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TMPDIR=" + temp}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []struct{ Args, MountPoints []string }
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	normalize := func(args []string) []string {
		out := append([]string{}, args...)
		seen := map[string]string{}
		for i, path := range out {
			if strings.HasPrefix(path, filepath.Join(temp, "claude-empty-")) {
				stat, e := os.Stat(path)
				if e != nil || !stat.IsDir() {
					t.Fatal("original empty-dir mount source not physically created", path, e)
				}
				if _, ok := seen[path]; !ok {
					seen[path] = "<empty-dir-" + string(rune('0'+len(seen))) + ">"
				}
				out[i] = seen[path]
			}
		}
		return out
	}
	for i, row := range rows {
		f := &LinuxFilesystem{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, TemporaryDirectory: temp, RipgrepCommand: rg, SearchDepth: row.Depth, AllowGitConfig: row.AllowGitConfig}
		args, e := f.Arguments(context.Background(), row.Read, row.Write)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(normalize(source[i].Args), normalize(args)) || !reflect.DeepEqual(source[i].MountPoints, f.mountPoints) {
			t.Fatalf("actual Source%d args %v mounts%v; native%v mounts%v", i, source[i].Args, source[i].MountPoints, args, f.mountPoints)
		}
	}
}
