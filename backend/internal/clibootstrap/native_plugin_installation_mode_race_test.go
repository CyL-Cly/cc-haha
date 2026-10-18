package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePluginInstallationKeepsAdmittedModeWhileOwnerStateChanges(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless-to-interactive", true: "interactive-to-headless"}[interactive], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var admitted, released sync.Once
			var requests atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("unexpected marketplace method: %s", r.Method)
				}
				admitted.Do(func() { close(entered) })
				select {
				case <-release:
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"name":"mkt","owner":{"name":"Owner"},"plugins":[]}`)
				case <-r.Context().Done():
				}
			}))
			defer peer.Close()
			defer released.Do(func() { close(release) })
			f := startupPluginFixture(t, "up-to-date")
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-plugin-admission-fake-key"
			// Change an already materialized source, so the genuine reconciler
			// reports Updated (not Installed). The interactive branch advertises
			// needsRefresh; the headless branch refreshes its active components.
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{
				"enabledPlugins":         map[string]bool{"sample@mkt": false},
				"extraKnownMarketplaces": map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "url", "url": peer.URL}}},
				"futureSettings":         map[string]bool{"keep": true},
			})))
			before, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Read"}, Model: "fixture-model", MaxTurns: 1, SystemPrompt: "private plugin admission without inference"}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "999.0.0-plugin-reconciliation", Interactive: interactive, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("real marketplace GET never reached its external hold", ctx.Err())
			}
			state := m.nativePluginInstallation
			if state == nil || !state.marketplaceEnabled {
				t.Fatal("actual startup plugin producer was not admitted")
			}
			if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				// SessionState's queue gate remains a live Owner consumer. Its
				// mode change cannot replace the already admitted plugin task.
				options := m.nativeSessionStateOptions()
				probe := func(mode bool) error {
					m.nativeSDKStateEvents = nil
					if err := options.EnqueueSDK(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("session_state_changed")}, protocol.JSONField{Name: "state", Value: quoteMain("running")})); err != nil {
						return err
					}
					want := 1
					if mode {
						want = 0
					}
					if len(m.nativeSDKStateEvents) != want {
						return fmt.Errorf("live SDK queue ignored actual interactive=%t: got %d want %d", mode, len(m.nativeSDKStateEvents), want)
					}
					m.nativeSDKStateEvents = nil
					return nil
				}
				if err := probe(interactive); err != nil {
					return err
				}
				m.options.Interactive = !interactive
				return probe(!interactive)
			}); err != nil {
				t.Fatal(err)
			}
			released.Do(func() { close(release) })
			select {
			case <-state.done:
			case <-ctx.Done():
				t.Fatal("actual plugin install/mount failed to complete", ctx.Err())
			}
			state.mu.Lock()
			installErr := state.err
			state.mu.Unlock()
			if installErr != nil {
				t.Fatal("actual startup task failed", installErr)
			}
			status, err := m.NativePluginInstallationStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if string(status.Get("needsRefresh")) != fmt.Sprint(interactive) {
				t.Fatalf("admitted startup branch changed after external GET: initial interactive=%t actual status=%s", interactive, rawMain(status))
			}
			var rows []nativeMarketplaceInstallation
			if err = json.Unmarshal(status.Get("marketplaces"), &rows); err != nil {
				t.Fatal(err)
			}
			if interactive {
				if len(rows) != 1 || rows[0].Name != "mkt" || rows[0].Status != "installed" || rows[0].Error != "" {
					t.Fatalf("interactive progress was not retained: %+v", rows)
				}
			} else if len(rows) != 0 {
				t.Fatalf("headless task acquired interactive progress: %+v", rows)
			}
			knownBytes, err := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
			if err != nil {
				t.Fatal(err)
			}
			entry := mainObj(mainObj(knownBytes).Get("mkt"))
			if mainObj(entry.Get("source")).String("url") != peer.URL {
				t.Fatalf("actual marketplace mapping was not updated: %s", knownBytes)
			}
			if _, err = os.Stat(entry.String("installLocation")); err != nil {
				t.Fatal("actual catalog was not persisted", err)
			}
			after, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil || !bytes.Equal(before, after) || requests.Load() != 1 {
				t.Fatal("startup rewrote declared intent or repeated physical GET", err, requests.Load())
			}
			if len(m.Controller.History()) != 0 {
				t.Fatal("plugin admission regression ran a model turn")
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
