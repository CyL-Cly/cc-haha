package computeruse

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWindowsInstalledAppsActualProcessAndCloseJoin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture; Windows host evidence remains separate")
	}
	o := fixtureOptions(t)
	o.Platform = "win32"
	root := filepath.Join(o.ConfigDir, ".runtime")
	python := filepath.Join(root, "venv/Scripts/python.exe")
	helper := filepath.Join(root, "win_helper.py")
	if err := os.MkdirAll(filepath.Dir(python), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("owned-helper"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
test "$PYTHONIOENCODING" = utf-8 || exit 17
test "$PYTHONUTF8" = 1 || exit 18
test "$1" = "$CLAUDE_CONFIG_DIR/.runtime/win_helper.py" || exit 19
test "$2" = list_installed_apps || exit 20
printf '%s\n' started > "$HOME/apps-started"
if test -f "$HOME/apps-hold"; then
 while test ! -f "$HOME/apps-release"; do sleep 0.05; done
fi
printf '%s\n' '{"ok":true,"result":[{"bundleId":"fixture.exe","displayName":"真实子进程","path":"C:\\fixture.exe","extra":42}]}'
`
	if err := os.WriteFile(python, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := fixtureService(t, o)
	status, body := request(t, s, "GET", "apps", "")
	value, ok := decodeObject(body)
	if status != 200 || !ok || string(value["apps"]) != `[{"bundleId":"fixture.exe","displayName":"真实子进程","path":"C:\\fixture.exe","extra":42}]` {
		t.Fatalf("actual helper process: %d %s", status, body)
	}
	started := filepath.Join(o.HomeDir, "apps-started")
	if err := os.Remove(started); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.HomeDir, "apps-hold"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	joined := make(chan []byte, 1)
	go func() { joined <- s.windowsInstalledApps(context.Background()) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !exists(started) {
		select {
		case <-deadline.C:
			t.Fatal("actual helper process did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal("Close did not join helper", err)
	}
	select {
	case result := <-joined:
		if string(result) != "[]" {
			t.Fatal("cancelled operation published apps", string(result))
		}
	default:
		t.Fatal("Close returned before admitted apps operation ended")
	}
	if err := os.Remove(started); err != nil {
		t.Fatal(err)
	}
	if result := s.windowsInstalledApps(context.Background()); string(result) != "[]" || exists(started) {
		t.Fatal("closed service admitted helper process", string(result))
	}
}
