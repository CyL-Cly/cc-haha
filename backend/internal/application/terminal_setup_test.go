package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualTerminalSetupEntryWritesConfigurationRetainsOldGlobalAndNextHarness(t *testing.T) {
	for _, terminal := range []string{"vscode", "ghostty", "tmux", "Apple_Terminal"} {
		t.Run(terminal, func(t *testing.T) {
			if terminal == "Apple_Terminal" && runtime.GOOS != "darwin" {
				t.Skip("actual macOS plist parser required")
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				calls.Add(1)
				if !strings.Contains(string(data), "/terminal-setup") {
					t.Error("canonical command absent from next actual request")
				}
				response(w, "terminal-setup-next", "", nil, "TERMINAL_SETUP_NEXT_HARNESS")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			for key, value := range map[string]string{"TERM_PROGRAM": terminal, "NO_COLOR": "1", "NO_PROXY": "127.0.0.1,localhost"} {
				o.Environment[key] = value
				o.Native.Environment[key] = value
			}
			if terminal == "Apple_Terminal" {
				privateBin := filepath.Join(o.Native.Home, "privatebin")
				if err := os.MkdirAll(privateBin, 0700); err != nil {
					t.Fatal(err)
				}
				seed := filepath.Join(o.Native.Home, "owned-seed.plist")
				if err := os.WriteFile(seed, []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Window Settings</key><dict><key>Basic</key><dict></dict></dict></dict></plist>`), 0600); err != nil {
					t.Fatal(err)
				}
				defaults := "#!/bin/sh\n[ \"$2\" = com.apple.Terminal ] || exit 81\ncase \"$1\" in\nexport) /bin/mkdir -p \"$HOME/Library/Preferences\"; /bin/cp '" + seed + "' \"$3\";;\nread) printf 'Basic\\n';;\nimport) /bin/cp \"$3\" \"$HOME/Library/Preferences/com.apple.Terminal.plist\";;\n*) exit 82;;\nesac\n"
				killall := "#!/bin/sh\n[ \"$1\" = cfprefsd ] || exit 83\nprintf 'owned\\n' >> \"$HOME/owned-killall-count\"\n"
				for name, data := range map[string]string{"defaults": defaults, "killall": killall} {
					if err := os.WriteFile(filepath.Join(privateBin, name), []byte(data), 0700); err != nil {
						t.Fatal(err)
					}
				}
				// The actual MainPrint executor must use its captured launch PATH.
				// No real defaults domain or cfprefsd process reaches this fixture.
				o.Environment["PATH"] = privateBin
				o.Native.Environment["PATH"] = privateBin
			}
			if err := os.WriteFile(filepath.Join(o.Native.CWD, "CLAUDE.md"), []byte("owned onboarding fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				return s.process.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					next.Set("retainedTerminalSetup", []byte(`{"old":true,"unknown":"retained"}`))
					next.Set("shiftEnterKeyBindingInstalled", []byte("false"))
					return &next, nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			if err = s.Submit(context.Background(), tui.Input{TurnID: newID(), Text: "/terminal-setup", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
				t.Fatal("normal terminal setup input", err)
			}
			if calls.Load() != 0 {
				t.Fatal("local setup requested a model", calls.Load())
			}
			global, err := s.process.Global.Get()
			var retained protocol.JSONObject
			if err != nil || retained.UnmarshalJSON(global.Get("retainedTerminalSetup")) != nil || string(raw(retained)) != `{"old":true,"unknown":"retained"}` {
				t.Fatal("old global fixture changed", err, string(global.Get("retainedTerminalSetup")))
			}
			path := filepath.Join(o.Native.Home, "Library", "Application Support", "Code", "User", "keybindings.json")
			data, readErr := os.ReadFile(path)
			if terminal == "vscode" {
				if readErr != nil || !strings.Contains(string(data), "workbench.action.terminal.sendSequence") || string(global.Get("shiftEnterKeyBindingInstalled")) != "true" {
					t.Fatal("actual persistent shortcut consumer", readErr, string(data), string(global.Get("shiftEnterKeyBindingInstalled")))
				}
				project, err := s.process.Global.GetProject(o.Native.CWD)
				if err != nil || string(project.Get("hasCompletedProjectOnboarding")) != "true" {
					t.Fatal("actual project onboarding consumer", err, project.Get("hasCompletedProjectOnboarding"))
				}
			} else if terminal == "Apple_Terminal" {
				if !os.IsNotExist(readErr) || string(global.Get("optionAsMetaKeyInstalled")) != "true" || string(global.Get("appleTerminalSetupInProgress")) != "false" || string(global.Get("shiftEnterKeyBindingInstalled")) != "false" {
					t.Fatal("actual Apple persistent consumers", readErr, global.Get("optionAsMetaKeyInstalled"), global.Get("appleTerminalSetupInProgress"))
				}
				plist := filepath.Join(o.Native.Home, "Library", "Preferences", "com.apple.Terminal.plist")
				data, err := os.ReadFile(plist)
				backup, backupErr := os.ReadFile(plist + ".bak")
				killall, killErr := os.ReadFile(filepath.Join(o.Native.Home, "owned-killall-count"))
				if err != nil || backupErr != nil || killErr != nil || string(killall) != "owned\n" || !strings.Contains(string(data), "useOptionAsMetaKey") || !strings.Contains(string(data), "<true/>") || !strings.Contains(string(data), "Bell") || !strings.Contains(string(data), "<false/>") || strings.Contains(string(backup), "useOptionAsMetaKey") {
					t.Fatal("actual private plist and OS consumers", err, backupErr, killErr, string(data), string(backup), string(killall))
				}
			} else if !os.IsNotExist(readErr) || string(global.Get("shiftEnterKeyBindingInstalled")) != "false" {
				t.Fatal("unsupported/native terminal mutated configuration", readErr, string(global.Get("shiftEnterKeyBindingInstalled")))
			}
			if err = s.Submit(context.Background(), tui.Input{TurnID: newID(), Text: "Continue after terminal setup.", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil || calls.Load() != 1 {
				t.Fatal("actual next Harness", err, calls.Load())
			}
		})
	}
}
