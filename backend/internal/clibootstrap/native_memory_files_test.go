package clibootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMemoryFilesResolveOnlyAtOSBoundaryAndKeepFiniteGuard(t *testing.T) {
	n := processNativeForTest(t)
	files := nativeMemoryFiles{n: n}
	requested := filepath.Join("relative-memory", "nested")
	if err := files.MkdirAll(requested); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(n.CWD, requested)
	if err := os.WriteFile(filepath.Join(physical, "MEMORY.md"), []byte("actual relative memory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := files.ReadFile(filepath.Join(requested, "MEMORY.md"))
	if err != nil || string(text) != "actual relative memory\n" {
		t.Fatal("read original requested memory against native CWD", err, string(text))
	}
	if info, err := files.Stat(requested); err != nil || !info.IsDir() {
		t.Fatal("actual requested memory directory", info, err)
	}
	if resolved, err := files.Realpath(requested); err != nil || resolved != physical {
		t.Fatal("actual native relative realpath", resolved, physical, err)
	}
	rows, err := files.ReadDir(requested)
	if err != nil || len(rows) != 1 || rows[0].Name() != "MEMORY.md" {
		t.Fatal("actual memory directory rows", rows, err)
	}
	finite := nativeMemoryFiles{n: nativeForTest(t)}
	if err := finite.MkdirAll(requested); err == nil {
		t.Fatal("finite mkdir must retain its canonical-path guard")
	}
	if _, err := finite.ReadFile(filepath.Join(requested, "MEMORY.md")); err == nil {
		t.Fatal("finite relative file must not become a native process capability")
	}
	if _, err := finite.ReadDir(physical); err == nil {
		t.Fatal("finite directory must not borrow another native root")
	}
}
