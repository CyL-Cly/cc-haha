package application

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestActualStatsPNGClipboardCancellationJoinsChildAndRemovesImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native screenshot primitive; PowerShell requires Windows")
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:9")
	o.Interactive = true
	bin := t.TempDir()
	started := filepath.Join(bin, "started")
	script := "#!/bin/sh\nprintf 'ready' > \"$SCREENSHOT_STARTED\"\nexec /bin/sleep 25\n"
	if err := os.WriteFile(filepath.Join(bin, "osascript"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin
		env["SCREENSHOT_STARTED"] = started
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- session.process.CopyTerminalStatsScreenshot(ctx, "Actual PNG child cancellation\n/stats")
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ready := false
	for !ready {
		if _, err = os.Stat(started); err == nil {
			ready = true
			break
		}
		select {
		case err := <-done:
			t.Fatal("clipboard worker exited before actual child", err)
		case <-deadline.C:
			t.Fatal("actual clipboard child not started")
		case <-time.After(10 * time.Millisecond):
		}
	}
	paths, _ := filepath.Glob(filepath.Join(o.Native.Temp, "claude-code-screenshots", "*.png"))
	if len(paths) != 1 {
		t.Fatal("real source screenshot input missing", paths)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled child unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled PNG clipboard worker/child did not join")
	}
	paths, _ = filepath.Glob(filepath.Join(o.Native.Temp, "claude-code-screenshots", "*.png"))
	if len(paths) != 0 {
		t.Fatal("cancelled screenshot did not remove temporary image", paths)
	}
}
