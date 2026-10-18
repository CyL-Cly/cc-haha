//go:build !windows

package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMountedStatusLineCommittedVisibilityCancelsHeldAndRemountsActualShell(t *testing.T) {
	s, home := newTerminalStatusLinePhysicalSession(t, "http://127.0.0.1:1", "if test -f \"$HOME/hold-statusline\"; then /bin/sleep 60; printf late > \"$HOME/forbidden-mount-late\"; fi\nprintf 'ACTUAL_MOUNT_TEXT'\n")
	changed := make(chan json.RawMessage, 20)
	initial, remove, err := s.SubscribeTerminalStatusLineMount(context.Background(), false, "NORMAL", func(value json.RawMessage) { changed <- append(json.RawMessage(nil), value...) })
	if err != nil || !strings.Contains(string(initial), `"configured":true`) {
		t.Fatal(err, string(initial))
	}
	defer remove()
	time.Sleep(350 * time.Millisecond)
	if _, err = os.Stat(filepath.Join(home, "statusline-records")); !os.IsNotExist(err) {
		t.Fatal("unmounted component spawned native command", err)
	}
	if err = s.SetTerminalStatusLineMounted(context.Background(), true, "NORMAL"); err != nil {
		t.Fatal(err)
	}
	waitTerminalStatusLinePhysicalRecord(t, home, 1)
	select {
	case value := <-changed:
		if !strings.Contains(string(value), "ACTUAL_MOUNT_TEXT") {
			t.Fatal(string(value))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mounted actual result absent")
	}
	if err = s.SetTerminalStatusLineMounted(context.Background(), false, "NORMAL"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "hold-statusline"), []byte("held"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTerminalStatusLineMounted(context.Background(), true, "INSERT"); err != nil {
		t.Fatal(err)
	}
	rows := waitTerminalStatusLinePhysicalRecord(t, home, 2)
	pid, err := strconv.Atoi(strings.SplitN(rows[1], " ", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	mounted := s.terminalStatusLine
	s.mu.Unlock()
	mounted.mu.Lock()
	presentation := string(mounted.presentation)
	mounted.mu.Unlock()
	if !strings.Contains(presentation, "ACTUAL_MOUNT_TEXT") {
		t.Fatal("remount erased original AppState text before held command settled", presentation)
	}
	if err = s.UpdateTerminalStatusLine(context.Background(), "NORMAL"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTerminalStatusLineMounted(context.Background(), false, "NORMAL"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("committed unmount retained actual shell", pid, err)
	}
	time.Sleep(350 * time.Millisecond)
	if data, err := os.ReadFile(filepath.Join(home, "statusline-records")); err != nil || len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 2 {
		t.Fatal("unmount failed to clear debounced command", err, string(data))
	}
	if _, err = os.Stat(filepath.Join(home, "forbidden-mount-late")); !os.IsNotExist(err) {
		t.Fatal("unmounted actual child had late side effect", err)
	}
	if err = os.Remove(filepath.Join(home, "hold-statusline")); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTerminalStatusLineMounted(context.Background(), true, "NORMAL"); err != nil {
		t.Fatal(err)
	}
	rows = waitTerminalStatusLinePhysicalRecord(t, home, 3)
	var callback map[string]any
	if err = json.Unmarshal([]byte(strings.SplitN(rows[2], " ", 2)[1]), &callback); err != nil {
		t.Fatal(err)
	}
	if callback["vim"].(map[string]any)["mode"] != "NORMAL" {
		t.Fatal("remount callback lost committed actual Vim mode", callback)
	}
	t.Log("actual initially-unmounted native component: 0 initial spawns, committed mount→held remount retained text→unmount retired PID and timer→fresh callback")
}
