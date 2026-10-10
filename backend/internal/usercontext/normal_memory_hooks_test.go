package usercontext

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNativeInstructionHooksApprovalProbeDoesNotConsumeEagerLoad(t *testing.T) {
	m, _ := nativeMemory(t)
	writeMemory(t, filepath.Join(m.Normal.CWD, "CLAUDE.md"), "parent @./child.md")
	writeMemory(t, filepath.Join(m.Normal.CWD, "child.md"), "child")
	type loaded struct{ path, reason string }
	events := make(chan loaded, 4)
	m.Normal.InstructionsLoaded = func(file MemoryFile, reason string) { events <- loaded{file.Path, reason} }
	if _, err := m.NormalMemoryFiles("claude-md", nil, true); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatal("approval probe must not fire instruction hook", event)
	default:
	}
	if _, err := m.NormalMemoryFiles("claude-md", nil, false); err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case event := <-events:
			reasons[filepath.Base(event.path)] = event.reason
		case <-time.After(time.Second):
			t.Fatal("missing actual eager hook")
		}
	}
	if reasons["CLAUDE.md"] != "session_start" || reasons["child.md"] != "include" {
		t.Fatal(reasons)
	}
	m.ClearMemoryFileCaches()
	if _, err := m.NormalMemoryFiles("claude-md", nil, false); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatal("cache-only clear must not replay eager hooks", event)
	default:
	}
	m.ResetMemoryFiles("compact")
	if _, err := m.NormalMemoryFiles("claude-md", nil, false); err != nil {
		t.Fatal(err)
	}
	reasons = map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case event := <-events:
			reasons[filepath.Base(event.path)] = event.reason
		case <-time.After(time.Second):
			t.Fatal("missing actual compact hook")
		}
	}
	if reasons["CLAUDE.md"] != "compact" || reasons["child.md"] != "include" {
		t.Fatal(reasons)
	}
}
