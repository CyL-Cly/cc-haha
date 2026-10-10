package application

import (
	"context"
	"encoding/pem"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTypedModelValidationCancelAndCloseJoinActualTLSHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		for _, action := range []string{"cancel", "close"} {
			t.Run(phase+"-"+action, func(t *testing.T) {
				entered, joined := make(chan struct{}), make(chan struct{})
				peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if phase == "body" {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", "100")
						w.WriteHeader(200)
						_, _ = w.Write([]byte(`{"unfinished":`))
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-r.Context().Done()
					close(joined)
				}))
				defer peer.Close()
				cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
				path := filepath.Join(t.TempDir(), "root.pem")
				if err := os.WriteFile(path, cert, 0600); err != nil {
					t.Fatal(err)
				}
				o := nativeSessionOptions(t, peer.URL)
				o.Arguments.Model = "sonnet"
				for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
					env["NODE_EXTRA_CA_CERTS"] = path
				}
				config := o.Environment["CLAUDE_CONFIG_DIR"]
				if err := os.MkdirAll(config, 0700); err != nil {
					t.Fatal(err)
				}
				settings := filepath.Join(config, "settings.json")
				original := []byte(`{"model":"sonnet","future":true}`)
				if err := os.WriteFile(settings, original, 0600); err != nil {
					t.Fatal(err)
				}
				s, err := NewSession(context.Background(), o)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				done := make(chan error, 1)
				go func() {
					done <- s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model Actual-Held-Custom"}, func(tui.Event) error { return nil })
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("actual model validation did not reach " + phase)
				}
				if action == "cancel" {
					s.Cancel()
				} else {
					closed := make(chan error, 1)
					go func() { closed <- s.Close(context.Background()) }()
					select {
					case err = <-closed:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("Close failed to join actual validation")
					}
				}
				select {
				case err = <-done:
					if err == nil || action == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("pending validation cancellation", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("actual validation producer did not settle")
				}
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Fatal("physical TLS peer remained owned after cancel/Close")
				}
				b, err := os.ReadFile(settings)
				if err != nil || string(b) != string(original) {
					t.Fatal("unvalidated model installed", string(b), err)
				}
				if action == "cancel" {
					if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model help"}, func(e tui.Event) error {
						if e.Kind == tui.Notice && !strings.Contains(e.Text, "selection menu") {
							t.Fatal(e.Text)
						}
						return nil
					}); err != nil {
						t.Fatal("same Owner failed after cancel", err)
					}
				}
			})
		}
	}
}
