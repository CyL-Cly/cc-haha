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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func preconnectMain(t *testing.T, endpoint string) *MainPrint {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_BASE_URL"] = endpoint
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-preconnect-model-key"
	main, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "private-preconnect", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "private preconnect", Tools: []string{""}, MaxTurns: 1, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(main.Close)
	return main
}

func TestNativeAPIPreconnectPhysicalOnceHeaderTimeoutAndClose(t *testing.T) {
	for _, kind := range []string{"once", "timeout", "close", "failure"} {
		t.Run(kind, func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodHead {
					t.Error("preconnect sent a model method", request.Method)
				}
				calls.Add(1)
				close(started)
				if kind == "timeout" || kind == "close" {
					<-request.Context().Done()
					close(cancelled)
					return
				}
				if kind == "failure" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer peer.Close()
			main := preconnectMain(t, peer.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			begin := time.Now()
			if err := main.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("actual HEAD was not sent", ctx.Err())
			}
			if err := main.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if kind == "close" {
				main.Close()
				if err := main.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrClosed) {
					t.Fatal("closed preconnect admitted another request", err)
				}
			}
			if err := main.joinNativeAPIPreconnect(ctx); err != nil {
				t.Fatal("physical preconnect worker did not join", err)
			}
			if kind == "timeout" && (time.Since(begin) < 9*time.Second || time.Since(begin) > 13*time.Second) {
				t.Fatal("actual Source 10s header timeout changed", time.Since(begin))
			}
			if kind == "timeout" || kind == "close" {
				select {
				case <-cancelled:
				case <-ctx.Done():
					t.Fatal("held HEAD socket survived timeout/close", ctx.Err())
				}
			}
			if calls.Load() != 1 {
				t.Fatal("once flag reset after completion/failure", calls.Load())
			}
		})
	}
}
