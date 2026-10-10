package settings

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) (*GitIgnore, string, map[string]string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("actual git is required for settings gitignore fixtures: ", err)
	}
	home := t.TempDir()
	env := map[string]string{
		"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL": filepath.Join(home, "empty-global.gitconfig"),
		"GIT_CONFIG_SYSTEM": filepath.Join(home, "empty-system.gitconfig"), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0", "PATH": filepath.Dir(git),
	}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	adapter, err := NewGitIgnore(GitIgnoreOptions{HomeDir: home, Environment: func() map[string]string { return env }, Executable: git})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, filepath.Join(home, "repo"), env
}

func initGit(t *testing.T, adapter *GitIgnore, repo string, env map[string]string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(adapter.options.Executable, "init", "-q", repo)
	command.Env = []string{}
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %s, %v", output, err)
	}
}

func TestActualGitignoreCreateAppendDeduplicateAndRespectIgnoreSources(t *testing.T) {
	for _, source := range []string{"create", "append", "existing substring", "repository", "info-exclude", "global"} {
		t.Run(source, func(t *testing.T) {
			adapter, repo, env := gitFixture(t)
			initGit(t, adapter, repo, env)
			seed(t, filepath.Join(repo, ".claude", "settings.local.json"), `{}`)
			global := filepath.Join(adapter.options.HomeDir, ".config", "git", "ignore")
			var before string
			switch source {
			case "append":
				before = "existing-rule\n"
				seed(t, global, before)
			case "existing substring":
				before = "# note **/.claude/settings.local.json is already listed here\n"
				seed(t, global, before)
			case "repository":
				seed(t, filepath.Join(repo, ".gitignore"), ".claude/settings.local.json\n")
			case "info-exclude":
				seed(t, filepath.Join(repo, ".git", "info", "exclude"), ".claude/settings.local.json\n")
			case "global":
				before = "**/.claude/settings.local.json\n"
				seed(t, global, before)
			}
			if err := adapter.AddFileGlobRule(testContext, ".claude/settings.local.json", repo); err != nil {
				t.Fatal(err)
			}
			if source == "repository" || source == "info-exclude" {
				if _, err := os.Stat(global); !errors.Is(err, fs.ErrNotExist) {
					t.Fatal("already ignored path wrote global file")
				}
				return
			}
			expected := "**/.claude/settings.local.json\n"
			if source == "append" {
				expected = before + "\n**/.claude/settings.local.json\n"
			} else if source == "global" || source == "existing substring" {
				expected = before
			}
			if actual := string(mustRead(t, global)); actual != expected {
				t.Fatalf("global ignore = %q, want %q", actual, expected)
			}
			if err := adapter.AddFileGlobRule(testContext, ".claude/settings.local.json", repo); err != nil {
				t.Fatal(err)
			}
			if actual := string(mustRead(t, global)); actual != expected {
				t.Fatal("duplicate ignore entry added")
			}
		})
	}
}

func TestGitignoreRepoRootDiscoveryDirectoryPatternAndMissingExecutable(t *testing.T) {
	adapter, repo, env := gitFixture(t)
	if directoryInGitRepo(repo) {
		t.Fatal("non-repository recognized")
	}
	if err := adapter.AddFileGlobRule(testContext, ".claude/settings.local.json", repo); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(adapter.options.HomeDir, ".config", "git", "ignore")
	if _, err := os.Stat(global); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("non-repository wrote global ignore")
	}
	initGit(t, adapter, repo, env)
	nested := filepath.Join(repo, "nested", "directory")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if !directoryInGitRepo(nested) {
		t.Fatal("nested git root not recognized")
	}
	seed(t, filepath.Join(repo, ".gitignore"), "cache/\n")
	if err := adapter.AddFileGlobRule(testContext, "cache/", repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(global); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("ignored directory wrote global ignore")
	}
	adapter.options.Executable = "definitely-no-fixture-git"
	if err := adapter.AddFileGlobRule(testContext, ".claude/settings.local.json", nested); err != nil {
		t.Fatal("missing git must be not-ignored, like execFileNoThrow: ", err)
	}
	if !strings.Contains(string(mustRead(t, global)), "**/.claude/settings.local.json") {
		t.Fatal("missing executable changed TS fail-open registration")
	}
	worktree := filepath.Join(adapter.options.HomeDir, "worktree")
	seed(t, filepath.Join(worktree, ".git"), "gitdir: fixture-not-real\n")
	if !directoryInGitRepo(worktree) {
		t.Fatal(".git file not recognized")
	}
}

func TestGitignoreEnvironmentExecutableLookupAndFilesystemErrors(t *testing.T) {
	adapter, repo, env := gitFixture(t)
	initGit(t, adapter, repo, env)
	name := filepath.Base(adapter.options.Executable)
	if got := findExecutable(name, env); got != adapter.options.Executable {
		t.Fatalf("injected PATH lookup = %s", got)
	}
	if got := findExecutable(name, map[string]string{"PATH": "."}); got != "" {
		t.Fatalf("relative ambient PATH used: %s", got)
	}
	adapter.options.Executable = name
	if err := adapter.AddFileGlobRule(testContext, "fixture", repo); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(adapter.options.HomeDir, ".config", "git", "ignore")
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(global, 0700); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AddFileGlobRule(testContext, "fixture", repo); err == nil {
		t.Fatal("global ignore read failure hidden")
	}
	if _, err := NewGitIgnore(GitIgnoreOptions{}); err == nil {
		t.Fatal("missing explicit bootstrap accepted")
	}
	if runtime.GOOS != "windows" {
		nonExecutable := filepath.Join(adapter.options.HomeDir, "fixture-nonexecutable")
		seed(t, nonExecutable, "plain text")
		if findExecutable(filepath.Base(nonExecutable), map[string]string{"PATH": filepath.Dir(nonExecutable)}) != "" {
			t.Fatal("non-executable command selected")
		}
	}
}

func TestActualLocalSettingsSaveRegistersRuleInInjectedHome(t *testing.T) {
	adapter, repo, env := gitFixture(t)
	initGit(t, adapter, repo, env)
	options := fixtureOptions(t)
	options.HomeDir, options.ConfigDir, options.ProjectRoot = adapter.options.HomeDir, filepath.Join(adapter.options.HomeDir, ".claude"), repo
	options.Environment = adapter.options.Environment
	options.GitIgnore, options.GitExecutable = nil, adapter.options.Executable
	service := mustService(t, options)
	if err := service.UpdateLocalSettings(testContext, object(`{"outputStyle":"Learning"}`), ""); err != nil {
		t.Fatal(err)
	}
	service.DrainBackground()
	if actual := string(mustRead(t, filepath.Join(options.HomeDir, ".config", "git", "ignore"))); actual != "**/.claude/settings.local.json\n" {
		t.Fatalf("real local-save ignore effect = %q", actual)
	}
	assertObject(t, fileObject(t, filepath.Join(repo, ".claude", "settings.local.json")), `{"outputStyle":"Learning"}`)
}
