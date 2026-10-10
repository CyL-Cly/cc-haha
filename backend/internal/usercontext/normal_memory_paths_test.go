package usercontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMemoryPathNativeIdentityTrustedSourcesAndCache(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "project_")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject())
	if err != nil {
		t.Fatal(err)
	}
	var setting string
	seen := []string{}
	paths := &MemoryPaths{Environment: env, Home: home, ConfigHome: filepath.Join(home, ".claude"), ProjectRoot: func() string { return root }, Files: OSMemoryFiles{}, Realpath: filepath.EvalSymlinks, SettingsForSource: func(source string) (*protocol.JSONObject, error) {
		seen = append(seen, source)
		if source == "userSettings" && setting != "" {
			s := protocol.NewJSONObject(protocol.JSONField{Name: "autoMemoryDirectory", Value: quote(setting)})
			return &s, nil
		}
		return nil, nil
	}}
	directory, err := paths.Directory()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".claude", "projects", projectpath.Sanitize(root), "memory") + string(filepath.Separator)
	if directory != want {
		t.Fatal(directory, want)
	}
	if strings.Join(seen, ",") != "policySettings,flagSettings,localSettings,userSettings" {
		t.Fatal("project settings must never redirect auto memory", seen)
	}
	setting = "~/custom-memory"
	same, err := paths.Directory()
	if err != nil || same != directory {
		t.Fatal("path must retain original project-root cache", same, err)
	}
	paths.Clear()
	directory, err = paths.Directory()
	if err != nil || directory != filepath.Join(home, "custom-memory")+string(filepath.Separator) {
		t.Fatal(directory, err)
	}
	setting = "~/foo/.."
	paths.Clear()
	directory, err = paths.Directory()
	if err != nil || directory != want {
		t.Fatal("HOME-wide directory must be rejected", directory, err)
	}
}
func TestCanonicalMemoryWorktreeRequiresStructureAndBacklink(t *testing.T) {
	home := t.TempDir()
	main := filepath.Join(home, "main")
	work := filepath.Join(main, "nested-work")
	entry := filepath.Join(main, ".git", "worktrees", "work")
	writeMemory(t, filepath.Join(work, ".git"), "gitdir: "+entry)
	writeMemory(t, filepath.Join(entry, "commondir"), "../..")
	resolvedWork, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	writeMemory(t, filepath.Join(entry, "gitdir"), filepath.Join(resolvedWork, ".git"))
	if root := CanonicalGitRoot(OSMemoryFiles{}, filepath.EvalSymlinks, work); root != main {
		t.Fatal(root, main)
	}
	writeMemory(t, filepath.Join(entry, "gitdir"), filepath.Join(main, ".git"))
	if root := CanonicalGitRoot(OSMemoryFiles{}, filepath.EvalSymlinks, work); root != work {
		t.Fatal("borrowed worktree backlink must not change trust/storage identity", root)
	}
}
