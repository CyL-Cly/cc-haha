package usercontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWorkingDirectoryRetainsContextPromiseAndClearsOnlyMemoryDiscovery(t *testing.T) {
	options := nativeOptions(t)
	path, _ := options.Settings.Path("userSettings", "")
	home := filepath.Dir(filepath.Dir(path))
	root := filepath.Join(home, "project")
	next := filepath.Join(root, "child")
	for _, dir := range []string{filepath.Dir(path), root, next} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("original parent instructions"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(next, "CLAUDE.md"), []byte("worktree child instructions"), 0600); e != nil {
		t.Fatal(e)
	}
	_ = options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), quote("false"))
	var mu sync.Mutex
	fired := 0
	normal := &NormalMemoryDiscovery{CWD: root, Home: home, ManagedRoot: filepath.Join(home, "managed"), NativeProcess: true, EnabledSources: []string{"projectSettings"}, ReadDir: NativeReadDir, Realpath: filepath.EvalSymlinks, InstructionsLoaded: func(MemoryFile, string) { mu.Lock(); fired++; mu.Unlock() }}
	options.Memory = &ExplicitUserMemory{ConfigHome: filepath.Dir(path), Profile: NormalEmptyMemoryProfile, Files: OSMemoryFiles{}, Normal: normal}
	service, e := New(options)
	if e != nil {
		t.Fatal(e)
	}
	first := service.GetUserContext()
	old, e := first.Await(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(old.Get("claudeMd")), "original parent instructions") || strings.Contains(string(old.Get("claudeMd")), "worktree child") {
		t.Fatal("initial actual memory", old)
	}
	mu.Lock()
	before := fired
	mu.Unlock()
	session := string(service.GetSessionStartDate())
	if e = service.SetWorkingDirectory(next); e != nil {
		t.Fatal(e)
	}
	if service.GetUserContext() != first || string(service.GetSessionStartDate()) != session {
		t.Fatal("Source Worktree invalidated context/date memo")
	}
	if service.options.Memory == options.Memory || service.options.Memory.Normal == normal || service.options.Memory.Normal.CWD != next {
		t.Fatal("in-flight original descriptor mutated")
	}
	// A separate original context cache invalidation is allowed to reload; the
	// Worktree clear itself never creates a second InstructionsLoaded hook fire.
	service.ClearUserContext()
	current, e := service.GetUserContext().Await(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(current.Get("claudeMd")), "worktree child instructions") {
		t.Fatal("new cwd discovery did not run", current)
	}
	mu.Lock()
	after := fired
	mu.Unlock()
	if after != before {
		t.Fatalf("spurious eager instruction hook: before%d after%d", before, after)
	}
}
