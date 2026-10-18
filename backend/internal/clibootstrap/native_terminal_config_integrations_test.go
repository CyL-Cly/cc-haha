package clibootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalConfigIDEAutoInstallSavedValueDrivesActualNextStartupProcess(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("physical private POSIX code executable")
	}
	var home string
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		home = n.Home
		bin := filepath.Join(home, "config-ide-bin")
		_ = os.MkdirAll(bin, 0700)
		n.Environment["TERM_PROGRAM"] = "vscode"
		n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "0"
		n.Environment["PATH"] = bin + ":/usr/bin:/bin"
		n.Environment["PRIVATE_CONFIG_IDE_HOME"] = home
		body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PRIVATE_CONFIG_IDE_HOME/code-calls.log\"\ncase \"$*\" in\n '--force --install-extension anthropic.claude-code') printf 'actual config startup installer' > \"$PRIVATE_CONFIG_IDE_HOME/extension-receipt.txt\" ;;\nesac\n"
		if err := os.WriteFile(filepath.Join(bin, "code"), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(map[string]any{"hasCompletedOnboarding": true, "autoInstallIdeExtension": false, "future": map[string]any{"keep": true}}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Interactive = true; o.Error = io.Discard })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := m.TerminalConfigState(ctx)
	if err != nil || string(state.Get("isSupportedTerminal")) != "true" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(home, "code-calls.log")); !os.IsNotExist(err) {
		t.Fatal("disabled startup executed code", err)
	}
	if err = m.SetTerminalConfigSetting(ctx, "autoInstallIdeExtension", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := NewMainPrint(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.ShutdownSession(context.Background())
	if err = next.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "extension-receipt.txt")
	for {
		if _, err = os.Stat(path); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("saved auto-install value did not start native process", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "code-calls.log"))
	if err != nil || !strings.Contains(string(data), "--force --install-extension anthropic.claude-code") {
		t.Fatal(string(data), err)
	}
	state, err = next.TerminalConfigState(ctx)
	if err != nil || string(nativeInProcessObject(nativeInProcessObject(state.Get("global")).Get("future")).Get("keep")) != "true" {
		t.Fatal(state, err)
	}
}

func TestTerminalConfigChromeDefaultUsesActualNextStartupEntitlementAndOldGlobal(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on-api-key-ineligible"}[enabled], func(t *testing.T) {
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
				_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(map[string]any{"hasCompletedOnboarding": true, "future": map[string]any{"keep": true}}), 0600)
			}, func(o *MainPrintOptions) { o.Interactive = true; o.Error = io.Discard })
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := m.SetTerminalConfigSetting(ctx, "claudeInChromeDefaultEnabled", rawMain(enabled)); err != nil {
				t.Fatal(err)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			next, err := NewMainPrint(m.options)
			if err != nil {
				t.Fatal(err)
			}
			defer next.ShutdownSession(context.Background())
			if err = next.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			next.joinNativeChromeInstallation()
			state, err := next.TerminalConfigState(ctx)
			global := nativeInProcessObject(state.Get("global"))
			if err != nil || string(global.Get("claudeInChromeDefaultEnabled")) != string(rawMain(enabled)) || string(nativeInProcessObject(global.Get("future")).Get("keep")) != "true" {
				t.Fatal(state, err)
			}
			if next.chromeInstallation != nil || next.nativeDynamicMCP.Get("claude-in-chrome") != nil || strings.Contains(next.options.Arguments.AppendSystemPrompt, "Chrome") {
				t.Fatal("API-key preference bypassed actual Source subscription entitlement")
			}
			if _, err = os.Stat(filepath.Join(next.options.Native.Environment["CLAUDE_CONFIG_DIR"], "chrome", "chrome-native-host")); !os.IsNotExist(err) {
				t.Fatal("ineligible startup installed host", err)
			}
		})
	}
}
