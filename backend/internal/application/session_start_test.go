package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCLIHookInitialInputBeforeEmptyStdinAndSDKStream(t *testing.T) {
	for _, mode := range []string{"text", "stream-json"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				calls.Add(1)
				if !bytes.Contains(body, []byte("ACTUAL_HOOK_INITIAL_A21")) || !bytes.Contains(body, []byte("ACTUAL_HOOK_CONTEXT_A21")) {
					t.Error("initial Hook input/history missing", string(body))
				}
				response(w, "initial-physical-response", "", nil, "ACTUAL_INITIAL_RESULT_A21")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			delete(o.Environment, "CLAUDE_CODE_SIMPLE")
			if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
				t.Fatal(err)
			}
			hook := `printf '%s' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"ACTUAL_HOOK_CONTEXT_A21","initialUserMessage":"ACTUAL_HOOK_INITIAL_A21"}}'`
			if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), raw(map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": hook}}}}}}), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--print", "--model", "fixture-model", "--system-prompt", "actual initial input fixture", "--tools", "", "--thinking", "disabled", "--output-format", mode}
			if mode == "stream-json" {
				args = append(args, "--input-format", "stream-json", "--verbose")
			}
			command, _, err := ParseCommand(args, o.Version, o.WorkDir, t.TempDir(), o.Environment)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var output bytes.Buffer
			code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
			if err != nil || code != 0 {
				t.Fatal(err, code, output.String())
			}
			if calls.Load() != 1 || !strings.Contains(output.String(), "ACTUAL_INITIAL_RESULT_A21") {
				t.Fatal("physical initial ASK", calls.Load(), output.String())
			}
		})
	}
}
