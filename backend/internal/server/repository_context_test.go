package server

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryContextNativeStatesAndUnbornGit(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	options.Environment = func() map[string]string {
		return map[string]string{"PATH": os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(options.HomeDir, "absent-global-git-config"), "GIT_OPTIONAL_LOCKS": "0"}
	}
	server, host := testHost(t, options)
	requestJSON(t, host, "GET", "/api/sessions/repository-context", "", 400)
	inspect := func(directory string) repositoryContext {
		raw := requestJSON(t, host, "GET", "/api/sessions/repository-context?workDir="+url.QueryEscape(directory), "", 200)
		var result repositoryContext
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err, string(raw))
		}
		return result
	}
	missing := filepath.Join(options.WorkDir, "missing")
	result := inspect(missing)
	if result.State != "missing_workdir" || result.Error != "Working directory does not exist: "+missing || result.RepoRoot != nil || len(result.Branches) != 0 || !server.filesystemAllowed(missing) {
		t.Fatal(result)
	}
	file := filepath.Join(options.WorkDir, "not-directory")
	if err := os.WriteFile(file, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	result = inspect(file)
	if result.State != "missing_workdir" || result.Error != "Working directory is not a directory: "+canonicalAccessPath(file) {
		t.Fatal(result)
	}
	result = inspect(options.WorkDir)
	if result.State != "not_git_repo" || result.WorkDir != canonicalAccessPath(options.WorkDir) || result.CurrentBranch != nil || result.Dirty || result.Branches == nil || result.Worktrees == nil {
		t.Fatal(result)
	}
	for _, directory := range []string{".git/objects", ".git/refs/heads"} {
		if err := os.MkdirAll(filepath.Join(options.WorkDir, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeSearchFixture(t, options.WorkDir, ".git/HEAD", "ref: refs/heads/fixture\n")
	writeSearchFixture(t, options.WorkDir, ".git/config", "[core]\n repositoryformatversion = 0\n bare = false\n")
	result = inspect(options.WorkDir)
	if result.State != "not_git_repo" {
		t.Fatal("Source negative Git root cache was silently refreshed", result)
	}
	fresh, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close(context.Background())
	result = fresh.inspectRepository(context.Background(), options.WorkDir)
	if result.State != "ok" || result.RepoRoot == nil || *result.RepoRoot != canonicalAccessPath(options.WorkDir) || result.RepoName == nil || *result.RepoName != "work" || result.CurrentBranch == nil || *result.CurrentBranch != "fixture" || result.DefaultBranch == nil || *result.DefaultBranch != "fixture" || result.HeadCommit != nil || !result.Dirty {
		t.Fatal(result)
	}
	if len(result.Branches) != 1 || result.Branches[0].Name != "fixture" || !result.Branches[0].Current || !result.Branches[0].Local || result.Branches[0].Remote || !result.Branches[0].CheckedOut || len(result.Worktrees) != 1 || !result.Worktrees[0].Current {
		t.Fatal("native Git worktree/current branch", result)
	}
	if _, err := os.Stat(filepath.Join(options.WorkDir, ".git/index")); !os.IsNotExist(err) {
		t.Fatal("readonly inspection wrote index", err)
	}
	if len(parseRepositoryWorktrees("worktree /missing\nHEAD abc\ndetached\n\nworktree /other\nbranch refs/heads/local\n")) != 2 {
		t.Fatal("worktree parser lost detached entry")
	}
}

func TestRepositoryCommandBoundAndFrozenExecutableResolution(t *testing.T) {
	output := &repositoryOutput{cancel: func() {}}
	if count, err := output.Write(make([]byte, 2000001)); err != nil || count != 2000001 || len(output.data) != 2000000 || !output.overflow {
		t.Fatal("maxBuffer did not bound native output", count, err, len(output.data))
	}
	if executable, err := executableUsingPath("git", map[string]string{"PATH": t.TempDir()}); err == nil || executable != "" {
		t.Fatal("native command escaped frozen PATH", executable, err)
	}
}

func TestGitRootCachesOriginalKeysAndEvictsLeastRecent(t *testing.T) {
	cache := newGitRootCache(2)
	calls := 0
	load := func(value string) func() string { return func() string { calls++; return value } }
	if cache.get("negative", load("")) != "" || cache.get("positive", load("root")) != "root" || cache.get("negative", load("changed")) != "" || calls != 2 {
		t.Fatal("negative cache lost identity", calls)
	}
	cache.get("third", load("new"))
	if cache.get("positive", load("updated")) != "updated" || calls != 4 {
		t.Fatal("LRU access order differs", calls)
	}
}
