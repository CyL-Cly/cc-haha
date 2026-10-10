package usercontext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalEmptyMemoryOriginalMissingFiles(t *testing.T) {
	m := &ExplicitUserMemory{ConfigHome: t.TempDir(), Profile: "source-normal-empty-memory-v1", Files: OSMemoryFiles{}, Normal: &NormalMemoryDiscovery{CWD: t.TempDir(), ManagedRoot: filepath.Join(t.TempDir(), "managed"), EnabledSources: []string{}, AutoMemoryEnabled: true, ReadDir: os.ReadDir}}
	v, e := m.load("claude-md", nil)
	if e != nil {
		t.Fatalf("normal empty discovery must execute missing files: %v", e)
	}
	if v != nil {
		t.Fatal(string(v))
	}
}

func TestNormalDefaultInstructionModePreservesEmptySourcesAndActiveFallbackCapability(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	m := &ExplicitUserMemory{ConfigHome: home, Profile: NormalEmptyMemoryProfile, Files: OSMemoryFiles{}, Normal: &NormalMemoryDiscovery{CWD: cwd, ManagedRoot: filepath.Join(t.TempDir(), "managed"), EnabledSources: []string{}, AutoMemoryEnabled: true, ReadDir: os.ReadDir}}
	mode := NormalizeInstructionFilesMode(nil)
	if mode != "claude-md-or-agents-md" {
		t.Fatal("Source default was changed", mode)
	}
	if v, e := m.load(mode, nil); e != nil || v != nil {
		t.Fatal("original default empty-source native path refused", v, e)
	}
	// A real active instruction file cannot be silently replaced by an empty
	// default-mode result when the source becomes enabled.
	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("active original instruction"), 0600)
	m.Normal.EnabledSources = []string{"projectSettings"}
	if _, e := m.load(mode, nil); e == nil {
		t.Fatal("unmigrated active AGENTS fallback silently erased")
	}
}
