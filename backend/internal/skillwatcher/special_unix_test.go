//go:build darwin || linux

package skillwatcher

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPhysicalFIFOIsExcludedWithoutOpeningOrBlockingIt(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "skills-pipe")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, exists := scan([]string{root})[fifo]; exists {
		t.Fatal("special file entered watched regular files")
	}
	w, e := Start(Options{Paths: []string{fifo, root}, Timing: &fixtureTiming, Reload: func(context.Context, string, bool) error { t.Error("FIFO emitted skill change"); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(40 * time.Millisecond)
	w.Close()
}
