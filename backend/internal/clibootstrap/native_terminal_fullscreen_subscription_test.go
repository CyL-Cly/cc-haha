package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalFullscreenSubscriptionActualSettingsEnvironmentWithoutMountedStatusLine(t *testing.T) {
	settings := protocol.NewJSONObject(fieldMain("statusLine", protocol.NewJSONObject(fieldMain("type", "command"), fieldMain("command", ""))), fieldMain("env", protocol.NewJSONObject(fieldMain("CLAUDE_CODE_NO_FLICKER", "0"))))
	f, m, shellReceipt := newNativeStatusLinePhysicalFixture(t, settings, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changes := make(chan struct{}, 4)
	initial, remove, err := m.SubscribeNativeTerminalFullscreen(ctx, func() { changes <- struct{}{} })
	if err != nil || initial || remove == nil {
		t.Fatal(initial, err)
	}
	defer remove()
	for _, flag := range []string{"1", "0"} {
		settings.Set("env", rawMain(protocol.NewJSONObject(fieldMain("CLAUDE_CODE_NO_FLICKER", flag))))
		path := filepath.Join(f.config, "settings.json")
		f.write(t, path, string(rawMain(settings)))
		if err = m.nativeSettingsFileChanged(ctx, "userSettings", path); err != nil {
			t.Fatal(err)
		}
		select {
		case <-changes:
		case <-ctx.Done():
			t.Fatal("Actual committed settings did not wake fullscreen", ctx.Err())
		}
		enabled, err := m.ReadTerminalFullscreen(ctx)
		if err != nil || enabled != (flag == "1") {
			t.Fatal("Current actual environment projection", flag, enabled, err)
		}
	}
	if data, err := os.ReadFile(shellReceipt); err != nil && !os.IsNotExist(err) || len(data) != 0 {
		t.Fatal("Fullscreen subscription executed unrelated statusline shell", err, string(data))
	}
	remove()
	settings.Set("env", rawMain(protocol.NewJSONObject(fieldMain("CLAUDE_CODE_NO_FLICKER", "1"))))
	path := filepath.Join(f.config, "settings.json")
	f.write(t, path, string(rawMain(settings)))
	if err = m.nativeSettingsFileChanged(ctx, "userSettings", path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("Unsubscribed fullscreen listener ran")
	default:
	}
	if value, err := m.ReadTerminalFullscreen(ctx); err != nil || !value {
		t.Fatal(value, err)
	}
}

func TestTerminalFullscreenIndependentReadUsesOneActualNativeTmuxMemo(t *testing.T) {
	settings := protocol.NewJSONObject(fieldMain("statusLine", protocol.NewJSONObject(fieldMain("type", "command"), fieldMain("command", ""))), fieldMain("env", protocol.NewJSONObject(fieldMain("CLAUDE_CODE_NO_FLICKER", "0"))))
	var probe string
	f, m, _ := newNativeStatusLinePhysicalFixture(t, settings, true, func(f nativePluginFixture) {
		probe = filepath.Join(f.home, "fullscreen-probes")
		script := filepath.Join(f.home, "tmux")
		f.write(t, script, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/fullscreen-probes\"\nprintf '1\\n'\n")
		if err := os.Chmod(script, 0700); err != nil {
			t.Fatal(err)
		}
		f.n.Environment["PATH"] = f.home + ":/usr/bin:/bin"
		f.n.Environment["TMUX"] = "actual-owned-read-probe"
		f.n.Environment["TERM_PROGRAM"] = ""
		f.n.Environment["TERM"] = "xterm-256color"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, flag := range []string{"", "1", ""} {
		settings.Set("env", rawMain(protocol.NewJSONObject(fieldMain("CLAUDE_CODE_NO_FLICKER", flag))))
		path := filepath.Join(f.config, "settings.json")
		f.write(t, path, string(rawMain(settings)))
		if err := m.nativeSettingsFileChanged(ctx, "userSettings", path); err != nil {
			t.Fatal(err)
		}
		got, err := m.ReadTerminalFullscreen(ctx)
		if err != nil || got != (flag == "1") {
			t.Fatal("Actual independent native read and memo", got, err)
		}
	}
	data, err := os.ReadFile(probe)
	if err != nil || string(data) != "display-message -p #{client_control_mode}\n" {
		t.Fatal("Actual process memo re-probed", err, string(data))
	}
}
