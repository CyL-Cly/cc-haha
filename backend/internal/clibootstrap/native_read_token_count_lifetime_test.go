package clibootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualLargeReadCountIgnoresASKAbortButRuntimeCloseCancelsAndJoins(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-interrupt", true: "runtime-close"}[closing], func(t *testing.T) {
			started, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if r.URL.Path != "/v1/messages/count_tokens" {
					t.Error("unexpected request", r.URL.Path)
				}
				close(started)
				select {
				case <-release:
					w.Header().Set("content-type", "application/json")
					io.WriteString(w, `{"input_tokens":42}`)
				case <-r.Context().Done():
					close(canceled)
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
			path := filepath.Join(m.options.Native.CWD, "admitted-long-history.txt")
			content := strings.Repeat("private", 4100)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			ask, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					_, err := m.nativeTools.Call(ask, "Read", webObject(`{"file_path":"admitted-long-history.txt"}`), scope, false)
					return err
				})
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("actual count HTTP never started")
			}
			cancel()
			if closing {
				m.BeginNativePDFRuntimeClose()
			} else {
				select {
				case err := <-done:
					t.Fatal("ordinary ASK abort prematurely settled admitted Read", err)
				case <-canceled:
					t.Fatal("ordinary ASK abort killed Source no-signal count")
				case <-time.After(40 * time.Millisecond):
				}
				close(release)
			}
			select {
			case err := <-done:
				if closing {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("runtime count did not cancel", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
				}
			case <-time.After(3 * time.Second):
				t.Fatal("count did not join actual native settlement")
			}
			if closing {
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("runtime close left actual HTTP alive")
				}
			} else {
				if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					state, present, err := m.nativeFileCache.Get(scope, path)
					if err != nil {
						return err
					}
					if !present || string(state.Content) != string(quoteMain(content)) {
						t.Error("ordinary interruption lost completed Read cache")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.ShutdownSession(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
