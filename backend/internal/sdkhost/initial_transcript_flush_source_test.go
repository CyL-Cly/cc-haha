package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The original full QueryEngine supplies the condition observations. The native
// arm executes the exact extracted Host persistence block against a real Record
// future/Writer. Its existing fixture scheduler is inert; the completion barrier
// follows a successful physical Write, so an omitted flush returns deterministically.
// The independent public MainPrint test covers the ordinary first-HTTP consumer.
func TestActualInitialTranscriptFlushConditionsMatchFullQueryEngine(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	profiles := []struct {
		name, simple, eager, cowork string
		sessionInput, flush         bool
	}{
		{name: "ordinary absent"},
		{name: "ordinary eager JS whitespace", eager: " \ufeffYeS ", flush: true},
		{name: "ordinary cowork", cowork: "on", flush: true},
		{name: "ordinary false values", eager: "false", cowork: "0"},
		{name: "ordinary OR", eager: "0", cowork: "true", flush: true},
		{name: "bare eager stays detached", simple: "1", eager: "1"},
		{name: "bare session input still flushes", simple: "1", sessionInput: true, flush: true},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{".claude", "tmp", "bin", "project"} {
				if err = os.MkdirAll(filepath.Join(home, directory), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`), 0600); err != nil {
				t.Fatal(err)
			}
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Error("unowned Source model request", r.Method, r.URL.String())
					http.Error(w, "unowned", http.StatusForbidden)
					return
				}
				body, err := io.ReadAll(r.Body)
				index := posts.Add(1)
				if err != nil || index != 1 || !bytes.Contains(body, []byte("ACTUAL_INITIAL_FLUSH_ACCEPTED_USER")) {
					t.Errorf("actual Source POST %d: %v %s", index, err, body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"initial-flush-model\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ACTUAL_INITIAL_FLUSH_MODEL_DONE\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer peer.Close()
			fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-initial-flush.fixture.ts")
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INITIAL_FLUSH_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(home, "bin"), "USE_BUILTIN_RIPGREP=false", "ANTHROPIC_API_KEY=offline-owned-initial-flush", "ANTHROPIC_BASE_URL=" + peer.URL, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_ATTRIBUTION_HEADER=0"}
			if profile.simple != "" {
				command.Env = append(command.Env, "CLAUDE_CODE_SIMPLE="+profile.simple)
			}
			if profile.eager != "" {
				command.Env = append(command.Env, "CLAUDE_CODE_EAGER_FLUSH="+profile.eager)
			}
			if profile.cowork != "" {
				command.Env = append(command.Env, "CLAUDE_CODE_IS_COWORK="+profile.cowork)
			}
			input, _ := json.Marshal(map[string]any{"home": home, "peer": peer.URL, "sessionInput": profile.sessionInput})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("actual Source initial flush setup/consumer: %v stdout=%s stderr=%s", err, output, stderr.String())
			}
			var source struct {
				Posts, FlushesBeforeFirstPost       int
				PersistedBeforeFirstPost, Persisted bool
				ExportPromisesSame                  []bool
				Results                             []struct {
					Subtype string
					IsError bool `json:"is_error"`
				}
				Receipts []struct{ Status string }
				Blocked  []string
			}
			if err = json.Unmarshal(output, &source); err != nil || stderr.Len() != 0 || len(source.Blocked) != 0 || posts.Load() != 1 || source.Posts != 1 || !source.Persisted || len(source.Results) != 1 || source.Results[0].Subtype != "success" || source.Results[0].IsError {
				t.Fatalf("actual Source full QueryEngine invalid: %v raw=%s stderr=%s", err, output, stderr.String())
			}
			expected := 0
			if profile.flush {
				expected = 1
			}
			if source.FlushesBeforeFirstPost != expected || profile.flush && !source.PersistedBeforeFirstPost {
				t.Fatalf("Source initial flush condition/durability: expected=%d raw=%s", expected, output)
			}
			for _, same := range source.ExportPromisesSame {
				if !same {
					t.Fatal("Source observer changed original export Promise", string(output))
				}
			}
			if profile.sessionInput && (len(source.Receipts) != 1 || source.Receipts[0].Status != "consumed") {
				t.Fatal("actual Source session input was not consumed", string(output))
			}

			files := newCleanupFiles()
			host, loader, path := cleanupHost(t, files)
			host.options.OriginalEnv = map[string]string{"CLAUDE_CODE_SIMPLE": profile.simple, "CLAUDE_CODE_EAGER_FLUSH": profile.eager, "CLAUDE_CODE_IS_COWORK": profile.cowork}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(files.release) }) }
			defer release()
			var record *sessioncontroller.RecordFuture
			done := make(chan error, 1)
			go func() {
				done <- host.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					entry := cleanupObject(`{"type":"user","uuid":"held-append","message":{"role":"user","content":"ACTUAL_INITIAL_FLUSH_ACCEPTED_USER"}}`)
					var err error
					record, err = host.pending.BeginRecordLive(scope, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &entry}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return nil }})
					if err != nil {
						return err
					}
					host.options.OnPhase("Record:returned")
					return host.awaitInitialTranscript(scope, record, profile.sessionInput)
				})
			}()
			if profile.flush {
				select {
				case <-files.entered:
				case err := <-done:
					t.Fatalf("actual initial persistence block returned without its required flush: %v SourceRaw=%s events=%v", err, output, files.snapshot())
				case <-ctx.Done():
					t.Fatal("initial flush never reached physical Write", context.Cause(ctx), files.snapshot())
				}
				select {
				case err := <-done:
					t.Fatal("initial flush returned before actual native Write completion", err)
				default:
				}
				disk, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(disk), "ACTUAL_INITIAL_FLUSH_ACCEPTED_USER") {
					t.Fatal("barrier did not follow a successful actual Write", err, string(disk))
				}
				release()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("actual initial persistence block", err)
				}
			case <-ctx.Done():
				t.Fatal("actual initial persistence block did not return", context.Cause(ctx))
			}
			if !profile.flush {
				// Joining the admitted Record proves the inert timer has no pending
				// pre-enqueue work before testing that this branch did not flush.
				if err := host.options.Owner.Run(ctx, func(scope *cooperative.Scope) error { _, err := record.Await(scope); return err }); err != nil {
					t.Fatal(err)
				}
				select {
				case <-files.entered:
					t.Fatal("initial persistence unexpectedly drained this control", string(output))
				default:
				}
				release()
			}
			t.Logf("full original QueryEngine flushBeforeFirstPOST=%d; actual Host initial block native completion held=%v", source.FlushesBeforeFirstPost, profile.flush)
		})
	}
}
