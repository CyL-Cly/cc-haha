package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalUsageFixtureOptions(t *testing.T, modelURL, proxy, ca string) Options {
	t.Helper()
	o := nativeSessionOptions(t, modelURL)
	o.Interactive = true
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "ANTHROPIC_API_KEY")
		env["HTTPS_PROXY"] = proxy
		env["NODE_EXTRA_CA_CERTS"] = ca
		env["USER_TYPE"] = "external"
		env["TZ"] = "UTC"
		env["NO_PROXY"] = "127.0.0.1,localhost"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"), raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-usage-token", "refreshToken": nil, "scopes": []string{"user:inference", "user:profile"}, "expiresAt": 9999999999999, "subscriptionType": "max"}}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]any{"billingType": "stripe_subscription"}, "future": 17}), 0600); err != nil {
		t.Fatal(err)
	}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	return o
}

func TestActualUsageSubmittedLocalMountOutlivesFinishedTurnAndEscJoinsHTTP(t *testing.T) {
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
	var id string
	emit := func(e tui.Event) error {
		if e.Kind == tui.LocalPanelOpened {
			id = e.LocalPanel.ID
		}
		return nil
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "/usage", Mode: "prompt"}, emit); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Mounted local Usage was canceled by completed Submit turn")
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "close"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("Actual local-mounted Usage HTTP did not join after Esc")
	}
}
func TestActualUsageHeldHTTPClosesOnEscAndGlobalCloseDuringModel(t *testing.T) {
	for _, name := range []string{"esc", "global-close-with-model"} {
		t.Run(name, func(t *testing.T) {
			started, joined := make(chan struct{}), make(chan struct{})
			var once sync.Once
			proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/oauth/usage" {
					t.Errorf("unowned API %s", r.URL)
					w.WriteHeader(404)
					return
				}
				once.Do(func() { close(started) })
				<-r.Context().Done()
				close(joined)
			}))
			modelStarted, modelJoined := make(chan struct{}), make(chan struct{})
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || !strings.Contains(string(body), "OWNED_HELD_MODEL") || r.Header.Get("Authorization") != "Bearer fake-usage-token" || r.Header.Get("x-api-key") != "" {
					t.Error("actual cached subscriber model boundary", err, string(body), r.Header)
				}
				close(modelStarted)
				<-r.Context().Done()
				close(modelJoined)
			}))
			defer model.Close()
			o := terminalUsageFixtureOptions(t, model.URL, proxy, ca)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			events := make(chan tui.Event, 16)
			emit := func(e tui.Event) error { events <- e; return nil }
			var submission chan error
			if name == "global-close-with-model" {
				submission = make(chan error, 1)
				go func() { submission <- s.Submit(context.Background(), tui.Input{Text: "OWNED_HELD_MODEL"}, nil) }()
				select {
				case <-modelStarted:
				case err := <-submission:
					t.Fatal("actual same owner model returned before HTTP", err)
				case <-time.After(2 * time.Second):
					t.Fatal("actual model did not start")
				}
			}
			if handled, err := s.InvokeTerminalCommand(context.Background(), "usage", "", emit); !handled || err != nil {
				t.Fatal(handled, err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("actual owned HTTP not started")
			}
			s.mu.Lock()
			panel := s.terminalPanel
			s.mu.Unlock()
			id := panel.props.ID
			finished := make(chan error, 1)
			go func() {
				if name == "esc" {
					finished <- s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "close"}, emit)
				} else {
					finished <- s.Close(context.Background())
				}
			}()
			select {
			case err := <-finished:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Usage teardown blocked on same owner or model")
			}
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Fatal("actual HTTP peer did not observe close")
			}
			if submission != nil {
				select {
				case <-submission:
				case <-time.After(time.Second):
					t.Fatal("model not joined")
				}
				select {
				case <-modelJoined:
				case <-time.After(time.Second):
					t.Fatal("physical model peer not closed")
				}
			}
			s.terminalUsage.mu.Lock()
			jobs := len(s.terminalUsage.jobs)
			s.terminalUsage.mu.Unlock()
			if jobs != 0 {
				t.Fatal("usage worker escaped session", jobs)
			}
			for len(events) > 0 {
				event := <-events
				if event.Kind == tui.LocalPanelRefreshed && event.LocalPanel != nil && !strings.Contains(string(event.LocalPanel.Presentation), `"loading":true`) {
					t.Fatal("canceled callback reopened Usage", event)
				}
			}
		})
	}
}

func TestActualUsageFiveSecondBudgetAndRetryResponseConsumer(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	joined := make(chan struct{})
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			<-r.Context().Done()
			close(joined)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":23.9,"resets_at":null}}`))
	}))
	o := terminalUsageFixtureOptions(t, "http://127.0.0.1:9", proxy, ca)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	events := make(chan tui.Event, 16)
	emit := func(e tui.Event) error { events <- e; return nil }
	start := time.Now()
	if handled, err := s.InvokeTerminalCommand(context.Background(), "usage", "", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	var panel *tui.LocalPanel
	deadline := time.NewTimer(7 * time.Second)
	defer deadline.Stop()
	for panel == nil {
		select {
		case event := <-events:
			if event.LocalPanel != nil && strings.Contains(string(event.LocalPanel.Presentation), "Failed to load usage data") {
				panel = event.LocalPanel
			}
		case <-deadline.C:
			t.Fatal("real5s error consumer missing")
		}
	}
	if elapsed := time.Since(start); elapsed < 4500*time.Millisecond || elapsed > 6*time.Second {
		t.Fatal("original budget altered", elapsed)
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("5s timeout left physical request alive")
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "usage-retry"}, emit); err != nil {
		t.Fatal(err)
	}
	ready := false
	for !ready {
		select {
		case event := <-events:
			if event.LocalPanel != nil && strings.Contains(strings.Join(tui.UsageVisualization(event.LocalPanel.Presentation, 80), "\n"), "23% used") {
				ready = true
			}
		case <-time.After(2 * time.Second):
			t.Fatal("real retry response not consumed")
		}
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatal("unexpected API/model calls", calls)
	}
}
