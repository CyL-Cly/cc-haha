package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestWholeSourceTerminalSetupActualEntryColorHyperlinksAndTheme(t *testing.T) {
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, force := range []string{"0", "1", "3"} {
			t.Run(theme+"/"+force, func(t *testing.T) {
				bun := os.Getenv("CC_HAHA_SOURCE_BUN")
				if bun == "" {
					t.Fatal("Pinned Source Bun required")
				}
				var calls atomic.Int32
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					response(w, "setup-style-next", "", nil, "STYLE_NEXT_DONE")
				}))
				defer peer.Close()
				o := nativeSessionOptions(t, peer.URL)
				o.Interactive, o.ForegroundShellProgress = true, true
				paintEnvironment := map[string]string{"TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.72", "TERM": "xterm-256color", "FORCE_COLOR": force}
				for name, value := range paintEnvironment {
					o.Environment[name], o.Native.Environment[name] = value, value
				}
				home := o.Native.Home
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-setup.fixture.ts")
				input, _ := json.Marshal(map[string]any{"home": home, "terminal": "vscode", "platform": "darwin", "theme": theme, "paint": map[string]any{"env": paintEnvironment, "platform": "darwin", "release": "", "tty": true, "args": []string{}}})
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				command.Dir = home
				command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_SETUP_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NO_COLOR=1", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
				command.Stdin = bytes.NewReader(input)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("whole actual Source painted setup %v %.5000s", err, output)
				}
				var source struct {
					Done  [][]string
					Error *string
				}
				if err = json.Unmarshal(output, &source); err != nil || source.Error != nil || len(source.Done) != 1 || len(source.Done[0]) != 1 {
					t.Fatalf("actual whole setup Source %v %.5000s", err, output)
				}
				path := filepath.Join(home, "Library", "Application Support", "Code", "User", "keybindings.json")
				if err = os.Remove(path); err != nil {
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
						next.Set("theme", raw(theme))
						return &next, nil
					})
				}); err != nil {
					t.Fatal(err)
				}
				if err = s.Submit(context.Background(), tui.Input{Text: "/terminal-setup", TurnID: "painted-setup", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
					t.Fatal(err)
				}
				actual := ""
				for _, record := range s.process.Host.History() {
					var message protocol.JSONObject
					_ = message.UnmarshalJSON(record.Record.Get("message"))
					var content string
					if json.Unmarshal(message.Get("content"), &content) == nil && strings.HasPrefix(content, "<local-command-stdout>") {
						actual = strings.TrimSuffix(strings.TrimPrefix(content, "<local-command-stdout>"), "</local-command-stdout>")
					}
				}
				if actual != source.Done[0][0] || calls.Load() != 0 {
					t.Fatalf("actual same normal command producer Go%q Source%q models%d", actual, source.Done[0][0], calls.Load())
				}
				if err = s.Submit(context.Background(), tui.Input{Text: "Continue after painted terminal setup", TurnID: "painted-next"}, nil); err != nil || calls.Load() != 1 {
					t.Fatal("next same Harness", err, calls.Load())
				}
			})
		}
	}
}
