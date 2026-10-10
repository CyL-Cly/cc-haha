package clisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalIgnoreMatchesActualSourceWithRealPrivateGit(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type scenario struct {
		Repo     bool    `json:"repo"`
		Nested   bool    `json:"nested"`
		Filename string  `json:"filename"`
		Global   *string `json:"global,omitempty"`
		Local    *string `json:"local,omitempty"`
		Repeat   int     `json:"repeat,omitempty"`
	}
	str := func(value string) *string { return &value }
	rows := []scenario{
		{Filename: ".claude/settings.local.json"},
		{Repo: true, Filename: ".claude/settings.local.json"},
		{Repo: true, Filename: ".claude/settings.local.json", Global: str("existing\n"), Repeat: 2},
		{Repo: true, Filename: ".claude/settings.local.json", Local: str(".claude/\n")},
		{Repo: true, Filename: ".claude/settings.local.json", Global: str("**/.claude/settings.local.json\n")},
		{Repo: true, Filename: ".claude/settings.local.json", Global: str("# substring **/.claude/settings.local.json extra\n")},
		{Repo: true, Filename: ".private-cache/"},
		{Repo: true, Nested: true, Filename: ".claude/settings.local.json"},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	env := func(home string) []string {
		return []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_GITIGNORE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=" + filepath.Join(home, "private.gitconfig")}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-permission-gitignore.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = env(home)
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, e := command.Output()
	if e != nil {
		t.Fatalf("Source Gitignore %v %.3000s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Content *string
			Calls   []map[string]any
		}
		Logs         []string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Rows) != len(rows) || len(source.Logs) > 0 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source result %v %.4000s", e, data)
	}
	for i, row := range rows {
		t.Run(row.Filename+string(rune('0'+i)), func(t *testing.T) {
			nativeHome := t.TempDir()
			root := filepath.Join(nativeHome, "repo")
			cwd := root
			if row.Nested {
				cwd = filepath.Join(root, "nested")
			}
			_ = os.MkdirAll(cwd, 0700)
			if row.Repo {
				git := exec.CommandContext(ctx, "/usr/bin/git", "init", "--quiet", root)
				git.Env = env(nativeHome)
				if out, e := git.CombinedOutput(); e != nil {
					t.Fatalf("private git %v %s", e, out)
				}
			}
			if row.Local != nil {
				_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(*row.Local), 0600)
			}
			global := filepath.Join(nativeHome, ".config/git/ignore")
			if row.Global != nil {
				_ = os.MkdirAll(filepath.Dir(global), 0700)
				_ = os.WriteFile(global, []byte(*row.Global), 0600)
			}
			calls := 0
			service, e := NewLocalIgnore(LocalIgnoreOptions{Home: nativeHome, Stat: os.Stat, ReadFile: os.ReadFile, MkdirAll: func(path string) error { return os.MkdirAll(path, 0777) }, WriteFile: func(path string, data []byte) error { return os.WriteFile(path, data, 0666) }, AppendFile: func(path string, data []byte) error {
				f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0666)
				if e != nil {
					return e
				}
				_, e = f.Write(data)
				closeErr := f.Close()
				if e == nil {
					e = closeErr
				}
				return e
			}, LogError: func(e error) { t.Errorf("native ignore %v", e) }, RunGit: func(ctx context.Context, dir string, args []string) (int, error) {
				calls++
				if dir != cwd || len(args) != 2 || args[0] != "check-ignore" {
					t.Fatal("native subprocess escaped")
				}
				git := exec.CommandContext(ctx, "/usr/bin/git", args...)
				git.Dir = dir
				git.Env = env(nativeHome)
				e := git.Run()
				if e == nil {
					return 0, nil
				}
				var exit *exec.ExitError
				if errors.As(e, &exit) {
					return exit.ExitCode(), nil
				}
				return 1, e
			}})
			if e != nil {
				t.Fatal(e)
			}
			repeat := row.Repeat
			if repeat == 0 {
				repeat = 1
			}
			for j := 0; j < repeat; j++ {
				if e = service.Run(ctx, row.Filename, cwd); e != nil {
					t.Fatal(e)
				}
			}
			actual, e := os.ReadFile(global)
			if source.Rows[i].Content == nil {
				if !os.IsNotExist(e) {
					t.Fatalf("unexpected ignore %q %v", actual, e)
				}
			} else if e != nil || string(actual) != *source.Rows[i].Content {
				t.Fatalf("Go%q Source%q %v", actual, *source.Rows[i].Content, e)
			}
			if calls != len(source.Rows[i].Calls) {
				t.Fatalf("git calls Go%d Source%d", calls, len(source.Rows[i].Calls))
			}
			for _, call := range source.Rows[i].Calls {
				if call["file"] != "git" || !strings.HasSuffix(call["cwd"].(string), map[bool]string{false: "repo-" + string(rune('0'+i)), true: "nested"}[row.Nested]) || call["preserveOutputOnError"] != false {
					t.Fatalf("Source primitive contract%v", call)
				}
			}
		})
	}
}
