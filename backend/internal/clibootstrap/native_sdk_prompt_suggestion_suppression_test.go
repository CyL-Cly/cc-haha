package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSDKPromptSuggestionSuppressionUsesRealTurnEnvironmentAndQuota(t *testing.T) {
	for _, profile := range []string{"early-conversation", "env-disabled", "plan", "cache-cold", "subscriber-warning", "subscriber-raw-warning", "api-key-warning"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			for key, value := range map[string]string{"ANTHROPIC_API_KEY": "fake-suppression", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USER_TYPE": "external"} {
				f.n.Environment[key] = value
			}
			if strings.HasPrefix(profile, "subscriber-") {
				delete(f.n.Environment, "ANTHROPIC_API_KEY")
				f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-suggestion-subscriber","scopes":["user:inference","user:profile"],"expiresAt":9999999999999,"subscriptionType":"max"}}`)
			}
			if profile == "env-disabled" {
				f.n.Environment["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"] = "false"
			}
			var roots, suggestions atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if bytes.Contains(body, []byte("[SUGGESTION MODE:")) {
					suggestions.Add(1)
					emitPlanFixture(w, 3, "", "run the tests")
					return
				}
				if strings.Contains(profile, "warning") {
					w.Header().Set("anthropic-ratelimit-unified-status", "allowed_warning")
					if profile != "subscriber-raw-warning" {
						w.Header().Set("anthropic-ratelimit-unified-5h-surpassed-threshold", "0.9")
					}
				}
				round := roots.Add(1)
				if profile == "cache-cold" && round == 2 {
					recorded := httptest.NewRecorder()
					emitPlanFixture(recorded, int(round), "", fmt.Sprintf("PARENT_RESULT_%d", round))
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write(bytes.ReplaceAll(recorded.Body.Bytes(), []byte(`"input_tokens":1`), []byte(`"input_tokens":11001`)))
					return
				}
				emitPlanFixture(w, int(round), "", fmt.Sprintf("PARENT_RESULT_%d", round))
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			results := make(chan struct{}, 3)
			var outputs atomic.Int32
			var diagnostics bytes.Buffer
			disabled := false
			mode := "default"
			if profile == "plan" {
				mode = "plan"
			}
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual suggestion suppression", MaxTurns: 6, Tools: []string{"Write"}, PermissionMode: mode, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
				var frame protocol.JSONObject
				if err := frame.UnmarshalJSON(data); err != nil {
					return err
				}
				if frame.String("type") == "result" {
					results <- struct{}{}
				}
				if frame.String("type") == "prompt_suggestion" {
					outputs.Add(1)
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			turns := 2
			if profile == "early-conversation" {
				turns = 1
			}
			inputDone := make(chan error, 1)
			go func() {
				if _, err := fmt.Fprintln(writer, `{"type":"control_request","request_id":"init","request":{"subtype":"initialize","promptSuggestions":true}}`); err != nil {
					inputDone <- err
					return
				}
				for at := 0; at < turns; at++ {
					if _, err := fmt.Fprintln(writer, string(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": fmt.Sprintf("PARENT_INPUT_%d", at)}}))); err != nil {
						inputDone <- err
						return
					}
					select {
					case <-results:
					case <-ctx.Done():
						inputDone <- ctx.Err()
						return
					}
				}
				inputDone <- writer.Close()
			}()
			if _, err = m.Run(ctx); err != nil {
				t.Fatal("actual suppression SDK consumer", err, diagnostics.String())
			}
			if err = <-inputDone; err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if profile == "api-key-warning" || profile == "subscriber-raw-warning" {
				want = 1
			}
			if roots.Load() != int32(turns) || suggestions.Load() != want || outputs.Load() != want {
				t.Fatal("suppression changed actual model/SDK effects", roots.Load(), suggestions.Load(), outputs.Load(), want)
			}
		})
	}
}
