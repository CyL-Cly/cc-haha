package skillcommands

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

type noInstalledSkills struct{}

func (noInstalledSkills) EnabledForSkills(context.Context) ([]skillregistry.Plugin, error) {
	return nil, nil
}

func TestInvalidationDuringSettingsLoadDoesNotDeadlockOrPublishOldSnapshot(t *testing.T) {
	home := t.TempDir()
	config, cwd, managed := filepath.Join(home, ".claude"), filepath.Join(home, "project"), filepath.Join(home, "managed")
	root := filepath.Join(config, "skills", "sample")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Old instructions")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	roots, err := skillregistry.New(skillregistry.Options{Home: home, ConfigDir: config, CWD: cwd, ProjectRoot: cwd, Environment: func(string) string { return "" }, Plugins: noInstalledSkills{}, Settings: func(context.Context) (protocol.JSONObject, error) {
		once.Do(func() { close(entered); <-release })
		return protocol.NewJSONObject(), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Home: home, ConfigDir: config, CWD: cwd, ManagedDir: managed, Roots: roots, Environment: func(string) string { return "" }, EnabledSource: func(context.Context, string) (bool, error) { return true, nil }, SkillsLocked: func(context.Context) (bool, error) { return false, nil }, AdditionalDirectories: func(context.Context) []string { return nil }, Bare: func() bool { return false }, InlinePluginCount: func() int { return 0 }, Plugins: func(context.Context) ([]pluginregistry.Loaded, error) { return nil, nil }, Gitignored: func(context.Context, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		definitions []Definition
		err         error
	}
	finished := make(chan result, 1)
	go func() {
		definitions, err := service.Local(context.Background(), cwd)
		finished <- result{definitions, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("settings callback was not entered")
	}
	cleared := make(chan struct{})
	go func() { service.Clear(); close(cleared) }()
	select {
	case <-cleared:
	case <-time.After(3 * time.Second):
		close(release)
		<-finished
		<-cleared
		t.Fatal("Skill load held its lock while awaiting shared settings; invalidation deadlocked")
	}
	write("New instructions")
	close(release)
	select {
	case got := <-finished:
		if got.err != nil || len(got.definitions) != 1 || got.definitions[0].Content() != "New instructions" {
			t.Fatalf("post-invalidation snapshot: %#v / %v", got.definitions, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("load did not finish after invalidation")
	}
	got, err := service.Local(context.Background(), cwd)
	if err != nil || len(got) != 1 || got[0].Content() != "New instructions" {
		t.Fatalf("cache retained stale snapshot: %#v / %v", got, err)
	}
}
