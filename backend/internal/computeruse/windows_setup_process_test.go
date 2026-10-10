package computeruse

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWindowsSetupActualProcessEnvironmentSuffixAndCloseJoin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture; Windows native host evidence remains separate")
	}
	o := fixtureOptions(t)
	o.Platform = "win32"
	bin := filepath.Join(o.HomeDir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(o.HomeDir, "started")
	released := filepath.Join(o.HomeDir, "released")
	script := `#!/bin/sh
test "$PYTHONIOENCODING" = utf-8 || exit 17
test "$PYTHONUTF8" = 1 || exit 18
if test "$1" = --version; then
  printf '%s\n' started > "$HOME/started"
  while test ! -f "$HOME/released"; do sleep 0.05; done
  printf '%s\n' 'Python 3.12.7'
  exit 0
fi
exit 19
`
	if err := os.WriteFile(filepath.Join(bin, "python3.exe"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o.Environment["PATH"] = bin + string(os.PathListSeparator) + "/usr/bin:/bin"
	o.Environment["PATHEXT"] = ".EXE"
	s := fixtureService(t, o)
	result := make(chan error, 1)
	go func() { _, err := s.WindowsSetup(context.Background()); result <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !exists(started) {
		select {
		case <-deadline.C:
			t.Fatal("real .exe fixture did not start with Python UTF8 environment")
		case <-time.After(10 * time.Millisecond):
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal("Close failed to join actual subprocess", err)
	}
	select {
	case <-result:
	default:
		t.Fatal("Close returned before setup operation joined")
	}
	if err := os.WriteFile(released, []byte("owned-test"), 0600); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(o.ConfigDir, ".runtime/venv-base-interpreter.txt")) || exists(filepath.Join(o.ConfigDir, ".runtime/requirements.sha256")) {
		t.Fatal("cancelled setup published successful markers")
	}
	_, err := s.WindowsSetup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatal("closed setup admitted operation", err)
	}
}
