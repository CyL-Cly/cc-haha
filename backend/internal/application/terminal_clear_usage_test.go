package application

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualClearSubmittedLocalCallerUnmountsHeldUsageAndRejectsStalePanel(t *testing.T) {
	started, joined := make(chan struct{}), make(chan struct{})
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(joined)
	}))
	o := terminalUsageFixtureOptions(t, "http://127.0.0.1:9", proxy, ca)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel string
	if err = s.Submit(context.Background(), tui.Input{Text: "/usage", Mode: "prompt"}, func(e tui.Event) error {
		if e.Kind == tui.LocalPanelOpened {
			panel = e.LocalPanel.ID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Actual Usage HTTP did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Submit(ctx, tui.Input{Text: "/clear", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal("Actual local clear blocked on unrelated component HTTP", err)
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("Clear did not join the actual local JSX unmount")
	}
	if s.terminalPanel != nil || s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel, Action: "usage-retry"}, nil) == nil {
		t.Fatal("Submitted clear retained a stale local panel consumer")
	}
}
