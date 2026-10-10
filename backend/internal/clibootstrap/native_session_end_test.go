package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSessionEndAfterCleanupPhysicalParallelOrderAndCancellation(t *testing.T) {
	for _, mode := range []string{"finish", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var journal string
			var stderr strings.Builder
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1/no-model", func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment["CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS"] = "100"
				if mode == "finish" {
					delete(n.Environment, "CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS")
				}
				journal = filepath.Join(n.CWD, "end-input.json")
				script := filepath.Join(n.CWD, "end-hook.sh")
				code := fmt.Sprintf("cat > %q\n", journal)
				if mode == "timeout" {
					code += "sleep 10\n"
				} else {
					code += "printf ACTUAL_SESSION_END_FAILURE_A21 >&2\nexit 7\n"
				}
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				settings := fmt.Sprintf(`{"hooks":{"SessionEnd":[{"matcher":"other","hooks":[{"type":"command","command":%q},{"type":"command","command":"printf ACTUAL_FAST_SECOND_A21 >&2;exit 6"},{"type":"prompt","prompt":"Source outside unsupported"}]}]}}`, "/bin/sh "+fmt.Sprintf("%q", script))
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) { o.Error = &stderr; o.Output = io.Discard })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(journal)
			if err != nil || !strings.Contains(string(data), `"reason":"other"`) {
				t.Fatal("actual SessionEnd process input", err, string(data), "stderr", stderr.String())
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatal("real cancellation did not drain child", elapsed)
			}
			if mode == "finish" && !strings.Contains(stderr.String(), "ACTUAL_SESSION_END_FAILURE_A21") {
				t.Fatal("actual stderr", stderr.String())
			}
			if mode == "finish" && strings.Index(stderr.String(), "ACTUAL_SESSION_END_FAILURE_A21") > strings.Index(stderr.String(), "ACTUAL_FAST_SECOND_A21") {
				t.Fatal("Promise.all original declaration order lost", stderr.String())
			}
			if !strings.Contains(stderr.String(), "Prompt stop hooks are not yet supported outside REPL") {
				t.Fatal("original outside unsupported business result", stderr.String())
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
