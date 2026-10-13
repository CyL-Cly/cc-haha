package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualNativeIDEExtensionInstallOnboardingPersistenceAndCloseJoinsPrivateProcess(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("actual private POSIX code executable, no Windows OS claim")
	}
	for _, profile := range []string{"install", "already-current", "failure", "blocked-close", "skip", "global-disabled"} {
		t.Run(profile, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("IDE extension operation must not request a model")
				w.WriteHeader(500)
			}))
			defer peer.Close()
			var home string
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				home = n.Home
				n.Environment["TERM_PROGRAM"] = "vscode"
				n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "0"
				if profile == "skip" {
					n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "true"
				}
				bin := filepath.Join(home, "ide-bin")
				_ = os.MkdirAll(bin, 0700)
				n.Environment["PATH"] = bin + ":/usr/bin:/bin"
				n.Environment["IDE_FIXTURE_HOME"] = home
				n.Environment["IDE_FIXTURE_PROFILE"] = profile
				script := `#!/bin/sh
printf '%s\n' "$*" >> "$IDE_FIXTURE_HOME/code-calls.log"
case "$*" in
 '--list-extensions') if [ "$IDE_FIXTURE_PROFILE" = already-current ]; then printf 'anthropic.claude-code\n'; fi ;;
 '--list-extensions --show-versions') if [ "$IDE_FIXTURE_PROFILE" = already-current ]; then printf 'anthropic.claude-code@99.0.0\n'; fi ;;
 '--force --install-extension anthropic.claude-code')
  if [ "$IDE_FIXTURE_PROFILE" = failure ]; then printf 'PRIVATE_INSTALL_FAILED\n' >&2; exit 7; fi
  if [ "$IDE_FIXTURE_PROFILE" = blocked-close ]; then printf '%s' "$$" > "$IDE_FIXTURE_HOME/code.pid"; sleep 100; fi
  printf 'INSTALLED_PHYSICAL_EXTENSION\n' > "$IDE_FIXTURE_HOME/extension-receipt.txt" ;;
esac
`
				if e := os.WriteFile(filepath.Join(bin, "code"), []byte(script), 0700); e != nil {
					t.Fatal(e)
				}
				global := map[string]any{"hasCompletedOnboarding": true, "diffTool": false, "future": map[string]any{"kept": "unknown"}}
				if profile == "global-disabled" {
					global["autoInstallIdeExtension"] = false
				}
				if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(global), 0600); e != nil {
					t.Fatal(e)
				}
			}, func(o *MainPrintOptions) { o.Interactive = true; o.Error = io.Discard })
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if profile == "blocked-close" {
				path := filepath.Join(home, "code.pid")
				for {
					if _, e := os.Stat(path); e == nil {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("private install process did not actually start")
					case <-time.After(10 * time.Millisecond):
					}
				}
				pidBytes, _ := os.ReadFile(path)
				pid, _ := strconv.Atoi(string(pidBytes))
				if pid <= 1 {
					t.Fatal("private actual process PID missing")
				}
				if e := m.ShutdownSession(ctx); e != nil {
					t.Fatal(e)
				}
				if _, e := os.Stat(filepath.Join(home, "extension-receipt.txt")); !os.IsNotExist(e) {
					t.Fatal("cancelled install published a false completion")
				}
				process, _ := os.FindProcess(pid)
				if e := process.Signal(syscall.Signal(0)); e == nil {
					t.Fatal("native install process survived joined close")
				}
				return
			}
			if profile == "skip" || profile == "global-disabled" {
				if e := m.ShutdownSession(ctx); e != nil {
					t.Fatal(e)
				}
				if _, e := os.Stat(filepath.Join(home, "code-calls.log")); !os.IsNotExist(e) {
					t.Fatal("disabled auto install executed private code")
				}
				return
			}
			var props *tui.IDEPicker
			for {
				e := m.Owner.Run(ctx, func(*cooperative.Scope) error {
					if m.ideUI != nil {
						if m.ideUI.pending != nil {
							copy := *m.ideUI.pending
							props = &copy
						}
						if m.ideUI.status != nil {
							return nil
						}
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
				settled := false
				_ = m.Owner.Run(ctx, func(*cooperative.Scope) error { settled = m.ideUI != nil && m.ideUI.status != nil; return nil })
				if settled {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("private extension result never reached same Owner")
				case <-time.After(10 * time.Millisecond):
				}
			}
			global, e := m.Global.Get()
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(global.Get("future")), "unknown") {
				t.Fatal("old unknown config was replaced", string(rawMain(global)))
			}
			if profile == "failure" {
				if props != nil || string(global.Get("diffTool")) != "false" {
					t.Fatal("failed install enabled onboarding/preferences", props)
				}
				return
			}
			if global.String("diffTool") != "auto" {
				t.Fatal("successful Source extension installation did not set diffTool default", string(rawMain(global)))
			}
			if profile == "already-current" {
				if props != nil {
					t.Fatal("already installed extension repeated onboarding")
				}
				if _, e := os.Stat(filepath.Join(home, "extension-receipt.txt")); !os.IsNotExist(e) {
					t.Fatal("newer extension was reinstalled")
				}
				return
			}
			if props == nil || props.Mode != "onboarding" {
				t.Fatal("actual new extension did not publish original onboarding")
			}
			if _, e := os.Stat(filepath.Join(home, "extension-receipt.txt")); e != nil {
				t.Fatal("owner ACK without actual installation", e)
			}
			if global.Get("hasIdeOnboardingBeenShown") != nil {
				t.Fatal("unrendered onboarding marked as shown")
			}
			if _, e := m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Presented: true}); e != nil {
				t.Fatal(e)
			}
			global, _ = m.Global.Get()
			shown := nativeInProcessObject(global.Get("hasIdeOnboardingBeenShown"))
			if string(shown.Get("vscode")) != "true" {
				t.Fatal("original per-terminal onboarding state not persisted")
			}
			if _, e := m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{}); e != nil {
				t.Fatal(e)
			}
			var pending *tui.IDEPicker
			_ = m.Owner.Run(ctx, func(*cooperative.Scope) error { pending = m.ideUI.pending; return nil })
			if pending != nil {
				t.Fatal("dismissed original onboarding remained pending")
			}
			if _, e := m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{}); e == nil {
				t.Fatal("retired onboarding callback accepted")
			}
		})
	}
}
