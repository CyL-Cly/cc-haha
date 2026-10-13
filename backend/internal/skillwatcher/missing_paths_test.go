package skillwatcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeMissingSkillRootReconcilesOnlyItsExactTreeAndUninstall(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "skills")
	var mu sync.Mutex
	var changed []string
	w, err := Start(Options{Paths: []string{path}, ReconcileMissingPaths: true, Timing: &fixtureTiming, Reload: func(_ context.Context, file string, dynamic bool) error {
		if dynamic || !strings.HasPrefix(file, path+string(filepath.Separator)) {
			t.Errorf("unrelated or invented change %q", file)
		}
		mu.Lock()
		changed = append(changed, file)
		mu.Unlock()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(changed) }
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("Do not watch the parent"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if count() != 0 {
		t.Fatal("missing-target reconciliation scanned parent or fabricated files")
	}
	file := filepath.Join(path, "first", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("First installed skill"), 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return count() == 1 })
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return count() == 2 })
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("Reinstalled skill"), 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return count() == 3 })
	w.Close()
	if err := os.WriteFile(file, []byte("after close"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if count() != 3 {
		t.Fatal("closed detector emitted")
	}
}
