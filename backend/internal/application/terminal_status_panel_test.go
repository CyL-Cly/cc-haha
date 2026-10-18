package application

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalStatusUsesActualSessionPropertiesAndNoDoctorReleaseNetwork(t *testing.T) {
	var requests, releaseRequests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		response(w, "status-next", "", nil, "STATUS_NEXT_DONE")
	}))
	defer peer.Close()
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releaseRequests.Add(1)
		http.Error(w, "Status must not probe releases", 500)
	}))
	defer release.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("disposable fixture keychain unavailable")
	}
	o.Interactive = true
	o.Native.Environment["CC_HAHA_CLI_RELEASE_API"] = release.URL
	_ = os.MkdirAll(filepath.Join(o.WorkDir, ".claude"), 0700)
	_ = os.WriteFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"), []byte(`{"language":"Deutsch","futureUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(o.WorkDir, "CLAUDE.md"), []byte(strings.Repeat("超", 40001)), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if handled, err := s.InvokeTerminalCommand(context.Background(), "status", "", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	labels := []string{}
	for _, row := range panel.Rows {
		labels = append(labels, row.Label)
	}
	text := strings.Join(labels, "\n")
	for _, expected := range []string{"Version: " + o.Version, "Session ID: " + s.process.Controller.SessionID(), "cwd: " + o.Native.CWD, "Model: fixture-model", "Anthropic base URL: " + peer.URL, "Setting sources: Project local settings"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing actual Status property", expected, text)
		}
	}
	if !strings.Contains(panel.Description, "40,001 chars > 40,000") || strings.Contains(panel.Description, "Channels") {
		t.Fatal("Source memory diagnostics not consumed", panel.Description)
	}
	if requests.Load() != 0 || releaseRequests.Load() != 0 {
		t.Fatal("local Status invoked model/Doctor release channels", requests.Load(), releaseRequests.Load())
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Continue after reading my actual status."}, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || releaseRequests.Load() != 0 {
		t.Fatal(requests.Load(), releaseRequests.Load())
	}
}

func TestTerminalStatusActualCachedFakeSubscriberAccountAndDefaultLabels(t *testing.T) {
	for _, kind := range []string{"pro", "max", "team"} {
		t.Run(kind, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			o.Arguments.Model = ""
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				env["ANTHROPIC_API_KEY"] = ""
				env["NODE_ENV"] = "production"
			}
			tier := ""
			if kind == "team" {
				tier = "default_claude_max_5x"
			}
			private := raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "owned-fake-never-sent", "refreshToken": "owned-fake-never-refreshed", "expiresAt": 2000000000000, "scopes": []string{"user:profile", "user:inference"}, "subscriptionType": kind, "rateLimitTier": tier}})
			secureReads := 0
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { secureReads++; return private, nil }
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]any{"emailAddress": "owned@example.invalid", "organizationName": "Owned Fixture Org"}, "futureAccount": map[string]any{"keep": true}}), 0600)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			value, err := s.process.TerminalStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			text := string(value.Get("properties"))
			label := "Claude " + strings.ToUpper(kind[:1]) + kind[1:] + " Account"
			expected := "Default Sonnet 5.5 · Best for everyday tasks"
			if kind != "pro" {
				expected = "Default Opus 5.5 with 1M context · Most capable for complex work"
			}
			for _, marker := range []string{label, expected, "Owned Fixture Org", "owned@example.invalid"} {
				if !strings.Contains(text, marker) {
					t.Fatal("actual cached account/default consumer", marker, text)
				}
			}
			if strings.Contains(text, "owned-fake") {
				t.Fatal("Status leaked credential value", text)
			}
			before := secureReads
			if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return s.process.Host.SetNativePermissionMode(scope, "default") }); err != nil {
				t.Fatal(err)
			}
			_, err = s.process.TerminalStatus(context.Background())
			if err != nil || secureReads != before {
				t.Fatal("Status bypassed shared credential cache", err, before, secureReads)
			}
		})
	}
}
