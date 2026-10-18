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
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestNativeInsightsParallelCancelClosesThreeActualBodiesAndKeepsNextASK(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	var started, closed atomic.Int32
	allStarted := make(chan struct{})
	allClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "CANCEL_INSIGHTS_") {
			if started.Add(1) == 3 {
				close(allStarted)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			if closed.Add(1) == 3 {
				close(allClosed)
			}
			return
		}
		emitWorktreeModel(w, 1, "", "", "normal after auxiliary cancel")
	}))
	defer server.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", MaxTurns: 2, Tools: []string{""}, Sources: DefaultState()}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	life, cancelLife := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancelLife()
	if e = m.StartSession(life); e != nil {
		t.Fatal(e)
	}
	active, cancel := context.WithCancel(life)
	done := make(chan error, 1)
	go func() {
		done <- m.Owner.Run(life, func(scope *cooperative.Scope) error {
			requests := []sdkhost.InsightsRequest{}
			for _, text := range []string{"CANCEL_INSIGHTS_1", "CANCEL_INSIGHTS_2", "CANCEL_INSIGHTS_3"} {
				requests = append(requests, sdkhost.InsightsRequest{Prompt: quoteMain(text), Model: "cancel-insights", MaxTokens: 500})
			}
			results, e := m.Host.QueryInsightsBatch(active, scope, requests)
			if e != nil {
				return e
			}
			if len(results) != 3 {
				t.Error("actual async batch response count", len(results))
			}
			for _, result := range results {
				if !errors.Is(result.Err, context.Canceled) {
					t.Error("actual model cancel identity", result.Err)
				}
			}
			return nil
		})
	}()
	select {
	case <-allStarted:
	case <-life.Done():
		cancel()
		t.Fatal("three physical auxiliary sockets did not overlap", started.Load())
	}
	cancel()
	select {
	case <-allClosed:
	case <-life.Done():
		t.Fatal("model cancellation left physical bodies alive", closed.Load())
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-life.Done():
		t.Fatal("parallel auxiliary continuations did not settle")
	}
	if e = m.RunTurn(life, webObject(`{"type":"user","message":{"role":"user","content":"normal after auxiliary cancel"}}`)); e != nil {
		t.Fatal(e)
	}
	if e = m.ShutdownSession(life); e != nil {
		t.Fatal(e)
	}
}
