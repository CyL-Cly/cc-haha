package clibootstrap

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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

func TestNativeModelFastStatusCloseJoinsActualTLSAndTokenReadWithoutLatePersistence(t *testing.T) {
	for _, phase := range []string{"headers", "body", "token-read"} {
		t.Run(phase, func(t *testing.T) {
			entered, joined := make(chan struct{}), make(chan struct{})
			var reads atomic.Int64
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if phase == "token-read" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":"owned 401"}`))
					return
				}
				if phase == "body" {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Length", "100")
					_, _ = w.Write([]byte(`{"enabled":`))
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
				close(joined)
			}))
			defer peer.Close()
			f := newNativePluginFixture(t)
			initial := []byte(`{"model":"opus","fastMode":true,"future":true}`)
			f.write(t, filepath.Join(f.config, "settings.json"), string(initial))
			// Keep the actual fast job isolated from main's separately tested
			// model migration, which legitimately changes old "opus" settings.
			f.write(t, filepath.Join(f.config, ".claude.json"), `{"penguinModeOrgEnabled":true,"future":true,"migrationVersion":11}`)
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
			certPath := filepath.Join(f.home, "root.pem")
			f.write(t, certPath, string(cert))
			env := f.n.Environment
			env["ANTHROPIC_API_KEY"] = "owned-fast-key"
			env["NODE_ENV"] = "test"
			env["NODE_EXTRA_CA_CERTS"] = certPath
			env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
			env["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
			f.n.RunSecurity = func(ctx context.Context, c securestorage.Command) ([]byte, error) {
				if phase == "token-read" && reads.Add(1) > 1 {
					close(entered)
					<-ctx.Done()
					close(joined)
					return nil, context.Cause(ctx)
				}
				return []byte(`{"claudeAiOauth":{"accessToken":"owned-token","refreshToken":"owned-refresh","scopes":["user:profile","user:inference"],"expiresAt":2000000000000,"subscriptionType":"max"},"future":true}`), nil
			}
			config, _ := desktopoauth.ResolveConfig(env)
			config.BaseAPIURL = peer.URL
			seedNativeReleaseWarmFixture(t, f, "fixture")
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Model: "opus", Tools: []string{"Read"}, MaxTurns: 8, PermissionMode: "default", SessionID: "66666666-6666-4666-8666-666666666666", SystemPrompt: "private fast Close"}, Native: f.n, PID: os.Getpid(), Platform: "darwin", Version: "fixture", Interactive: true, NativeModelFastConfig: &config, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err = m.StartSession(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("missing actual held phase", phase)
			}
			done := make(chan struct{})
			go func() { m.Close(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not join actual fast job", phase)
			}
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("physical producer escaped Close", phase)
			}
			saved, e := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if e != nil || string(saved) != string(initial) {
				t.Fatal("late status changed settings after Close", e, string(saved))
			}
			global, e := os.ReadFile(filepath.Join(f.config, ".claude.json"))
			var value map[string]any
			_ = json.Unmarshal(global, &value)
			if e != nil || value["penguinModeOrgEnabled"] != true || value["future"] != true {
				t.Fatal("late status changed original global fields", e, value)
			}
		})
	}
}
