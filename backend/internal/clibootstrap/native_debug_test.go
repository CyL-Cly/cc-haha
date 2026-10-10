package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualMainPrintDebugFlagsUseExistingAuthenticationErrorProducer(t *testing.T) {
	for _, stderrMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "stderr"}[stderrMode], func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-debug-auth-fake"
			f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			calls := 0
			f.n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				calls++
				return nil, errors.New("actual private debug\nKeychain primitive failed")
			}
			path := filepath.Join(f.config, "explicit", "debug.log")
			args := []string{"--debug=error", "--debug-file", path}
			if stderrMode {
				args = append(args, "-d2e")
				f.n.Environment["NODE_ENV"] = "test"
			}
			disabled := false
			var terminal bytes.Buffer
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{DebugArguments: args, DebugFormatted: true, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "No model in constructor proof", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &terminal})
			if e != nil {
				t.Fatal(e)
			}
			// Exercise the real legacy auth source, including its recoverable
			// primitive failure, rather than submitting a synthetic log record.
			calls = 0
			binding, e := m.NewNativeOpenAIAuth(nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				ctx, e := binding.owned(context.Background(), scope)
				if e != nil {
					return e
				}
				_, e = binding.apiKeySource(ctx, map[string]string{}, false, false)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			m.Close()
			var log []byte
			if stderrMode {
				log = terminal.Bytes()
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("stderr mode wrote file", e)
				}
			} else {
				log, e = os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if terminal.Len() != 0 {
					t.Fatal("file logging leaked stderr", terminal.String())
				}
			}
			if calls != 1 || !bytes.Contains(log, []byte(`[ERROR] "Error: actual private debug\nKeychain primitive failed"`)) || bytes.Contains(log, []byte("Error log sink initialized")) {
				t.Fatalf("existing native auth error producer/filter/format did not reach debug sink: calls%d %q", calls, log)
			}
		})
	}
}
