package usercontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMemoryNativeRequestedDirectoryKeepsProjectMemoAndFiniteFallback(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject())
	if err != nil {
		t.Fatal(err)
	}
	requested := "relative-config"
	paths := &MemoryPaths{Environment: environment, Home: home, ConfigHome: filepath.Join(home, ".claude"), ProjectRoot: func() string { return root }, Files: OSMemoryFiles{}, Realpath: filepath.EvalSymlinks, SettingsForSource: func(string) (*protocol.JSONObject, error) { return nil, nil }, NativeConfigDirectory: func() string { return requested }}
	first, err := paths.Directory()
	want := filepath.Join(requested, "projects", projectpath.Sanitize(root), "memory") + string(filepath.Separator)
	if err != nil || first != want {
		t.Fatal("original requested path on first miss", first, want, err)
	}
	requested = ""
	if same, err := paths.Directory(); err != nil || same != first {
		t.Fatal("same project must keep original cached path", same, err)
	}
	paths.Clear()
	want = filepath.Join("projects", projectpath.Sanitize(root), "memory") + string(filepath.Separator)
	if next, err := paths.Directory(); err != nil || next != want {
		t.Fatal("empty requested configuration is not an absent override", next, want, err)
	}
	paths.NativeConfigDirectory = nil
	paths.Clear()
	want = filepath.Join(paths.ConfigHome, "projects", projectpath.Sanitize(root), "memory") + string(filepath.Separator)
	if finite, err := paths.Directory(); err != nil || finite != want {
		t.Fatal("finite profile keeps original explicit absolute root", finite, want, err)
	}
}
