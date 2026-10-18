//go:build !windows

package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeLSPActualHeldInitializeIsCanceledAndReapedBySessionShutdown(t *testing.T) {
	var release *os.File
	f, m, receipt := newNativeLSPLifecycleFixture(t, false, false, false, func(f nativePluginFixture) map[string]string {
		path := filepath.Join(f.home, "initialize-release")
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		var err error
		release, err = os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"CC_HAHA_LSP_INITIALIZE_RELEASE": path}
	})
	defer release.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.nativeLSP.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.n.CWD, "held-initialize.txt")
	f.write(t, path, "physical LSP lifecycle input\n")
	opened := make(chan error, 1)
	go func() { opened <- m.nativeLSP.Open(path, quoteMain("physical LSP lifecycle input\n")) }()
	var pid int
	for pid == 0 {
		for _, row := range nativeLSPPhysicalFrames(t, receipt) {
			if row.String("method") == "initialize" {
				if err := json.Unmarshal(row.Get("pid"), &pid); err != nil {
					t.Fatal(err)
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual LSP initialize was not entered", context.Cause(ctx))
		case <-time.After(time.Millisecond):
		}
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("actual session shutdown", err)
	}
	select {
	case err := <-opened:
		if err == nil {
			t.Fatal("held LSP initialize completed despite no release")
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not join pending LSP initialize", context.Cause(ctx))
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("closed session left its actual LSP process alive", pid, err)
	}
	for _, row := range nativeLSPPhysicalFrames(t, receipt) {
		if row.String("method") != "initialize" {
			t.Fatal("canceled initialization published a late protocol notification", string(rawMain(row)))
		}
	}
	if m.nativeLSP.Enabled() || m.nativeLSP.Owns(m.Owner, m.nativeLSPLife.binding) {
		t.Fatal("closed process retained LSP authority")
	}
	if err := m.nativeLSP.JoinNotifications(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLSPActualPluginReloadReplacesPeerAndOpenStateOnSameManager(t *testing.T) {
	f, m, oldReceipt := newNativeLSPLifecycleFixture(t, false, false, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := m.nativeLSP
	if err := manager.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.n.CWD, "reload-space 中文.txt")
	f.write(t, path, "actual reload file\n")
	if err := manager.Open(path, quoteMain("actual reload file\n")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("old peer barrier", found, err)
	}
	oldPID := 0
	for _, row := range nativeLSPPhysicalFrames(t, oldReceipt) {
		if row.String("method") == "initialize" {
			if err := json.Unmarshal(row.Get("pid"), &oldPID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if oldPID == 0 || !manager.IsOpen(path) {
		t.Fatal("old actual manager was never started/opened")
	}
	newReceipt := filepath.Join(f.home, "reloaded-lsp-frames.jsonl")
	peer := filepath.Join(f.plugin, "bin", "private-lsp-peer")
	config := protocol.NewJSONObject(fieldMain("command", peer), fieldMain("args", []string{newReceipt, f.home}), fieldMain("workspaceFolder", f.n.CWD), fieldMain("extensionToLanguage", map[string]string{".txt": "plaintext"}))
	f.write(t, filepath.Join(f.plugin, ".lsp.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "fixture", Value: rawMain(config)}))))
	if _, err := m.ReloadComponents(ctx); err != nil {
		t.Fatal("actual component reload", err)
	}
	if m.nativeLSP != manager {
		t.Fatal("reload replaced the concrete writer/tool manager pointer")
	}
	if err := manager.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.JoinNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(oldPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("plugin reload left the old actual peer alive", oldPID, err)
	}
	if manager.IsOpen(path) {
		t.Fatal("reloaded server inherited an obsolete open-file set")
	}
	if _, err := os.Stat(newReceipt); !os.IsNotExist(err) {
		t.Fatal("reloaded configuration eagerly spawned the replacement", err)
	}
	rows := manager.State()
	if len(rows) != 1 || rows[0].String("state") != "stopped" {
		t.Fatal("replacement configuration was not installed", string(rawMain(rows)))
	}
	if err := manager.Open(path, quoteMain("actual reload file\n")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.Request(path, "$/fixture/barrier", nil); err != nil || !found {
		t.Fatal("replacement peer barrier", found, err)
	}
	newPID, opens := 0, 0
	for _, row := range nativeLSPPhysicalFrames(t, newReceipt) {
		if row.String("method") == "initialize" {
			if err := json.Unmarshal(row.Get("pid"), &newPID); err != nil {
				t.Fatal(err)
			}
		}
		if row.String("method") == "textDocument/didOpen" {
			opens++
		}
	}
	if newPID <= 0 || newPID == oldPID || opens != 1 || !manager.IsOpen(path) {
		t.Fatal("replacement actual peer/open-file state", oldPID, newPID, opens)
	}
	shutdowns := 0
	for _, row := range nativeLSPPhysicalFrames(t, oldReceipt) {
		if row.String("method") == "shutdown" {
			shutdowns++
		}
	}
	if shutdowns != 1 {
		t.Fatal("old peer did not receive its actual shutdown protocol", shutdowns)
	}
}
