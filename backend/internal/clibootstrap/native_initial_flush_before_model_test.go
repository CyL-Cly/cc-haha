package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// This public nonbare MainPrint consumer complements the deterministic held
// Write test of the exact Host branch. The peer reads the actual transcript at
// first POST admission; no lifecycle observer or filesystem replacement supplies
// its accepted user bytes. This is not a claim about bare priority/cold title.
func TestActualMainPrintInitialEagerAndCoworkFlushPrecedesFirstModelHTTP(t *testing.T) {
	for _, profile := range []struct{ name, key, value string }{
		{name: "eager", key: "CLAUDE_CODE_EAGER_FLUSH", value: "yes"},
		{name: "cowork", key: "CLAUDE_CODE_IS_COWORK", value: "true"},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
			marker := "ACTUAL_INITIAL_FLUSH_MAINPRINT_" + profile.name
			var target string
			var mu sync.Mutex
			var atPOST []byte
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Error("unowned HTTP", r.Method, r.URL.String())
					http.Error(w, "unowned", http.StatusForbidden)
					return
				}
				body, err := io.ReadAll(r.Body)
				index := posts.Add(1)
				if err != nil || index != 1 || !bytes.Contains(body, []byte(marker)) {
					t.Errorf("actual initial model POST %d: %v %s", index, err, body)
				}
				mu.Lock()
				path := target
				mu.Unlock()
				disk, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(disk, []byte(marker)) {
					t.Errorf("first actual model POST preceded accepted user durability: %v path=%s transcript=%s", err, path, disk)
				}
				mu.Lock()
				atPOST = append([]byte(nil), disk...)
				mu.Unlock()
				emitPlanFixture(w, int(index), "", "ACTUAL_INITIAL_FLUSH_MAINPRINT_DONE")
			}))
			defer peer.Close()
			delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
			f.n.Environment[profile.key] = profile.value
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			f.n.Environment["ANTHROPIC_API_KEY"] = "offline-owned-initial-flush"
			f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			disabled := false
			options := MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual initial flush", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}}
			m, err := NewMainPrint(options)
			if err != nil {
				t.Fatal(err)
			}
			// Register bounded cleanup immediately after the successful factory.
			// The business turn keeps its original separate 30s context.
			var closeOnce sync.Once
			closeNative := func() {
				closeOnce.Do(func() {
					closeContext, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer closeCancel()
					done := make(chan error, 1)
					go func() {
						err := m.ShutdownSession(closeContext)
						if err == nil {
							m.Close()
						}
						done <- err
					}()
					select {
					case err := <-done:
						if err != nil {
							t.Error("bounded native cleanup", err)
						}
					case <-closeContext.Done():
						t.Error("native cleanup exceeded 3s", closeContext.Err())
					}
				})
			}
			t.Cleanup(closeNative)
			defer closeNative()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
				mu.Lock()
				target = path
				mu.Unlock()
				return err
			}); err != nil {
				t.Fatal(err)
			}
			user := objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": marker}}))
			if err = m.RunTurn(ctx, user); err != nil {
				t.Fatal("actual public turn", err)
			}
			mu.Lock()
			durable := bytes.Contains(atPOST, []byte(marker))
			mu.Unlock()
			if posts.Load() != 1 || !durable {
				t.Fatal("actual first HTTP lacked accepted user transcript", posts.Load())
			}
			t.Logf("actual ordinary MainPrint %s: first loopback POST observed its accepted canonical user bytes", profile.name)
		})
	}
}
