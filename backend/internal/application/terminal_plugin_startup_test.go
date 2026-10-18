package application

import (
	"bytes"
	"context"
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

func TestActualPublicCLIHeadlessSyncPluginReconcileReachesFirstSkillModel(t *testing.T) {
	var requests atomic.Int32
	var body []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ = io.ReadAll(r.Body)
		requests.Add(1)
		response(w, "plugin-sync-first", "", nil, "PUBLIC_SYNC_PLUGIN_FIRST_MODEL")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	market := filepath.Join(o.WorkDir, "market")
	_ = os.Remove(filepath.Join(config, "plugins", "known_marketplaces.json"))
	initial := raw(map[string]any{"futureUser": map[string]bool{"keep": true}, "enabledPlugins": map[string]bool{"sample@mkt": true}, "extraKnownMarketplaces": map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}}}})
	if err := os.WriteFile(filepath.Join(config, "settings.json"), initial, 0600); err != nil {
		t.Fatal(err)
	}
	// This profile tests startup materialization and the first Skill consumer;
	// option forms and credentials are covered by their separate real dialogs.
	if err := os.WriteFile(filepath.Join(market, "plugins", "sample", ".claude-plugin", "plugin.json"), []byte(`{"name":"sample","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(market, "plugins", "sample", "skills", "sample", "SKILL.md"), []byte("---\ndescription: Startup installed actual Skill\n---\nACTUAL_NEW_MARKETPLACE_SKILL_FIRST"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"] = "1"
	o.Native.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"] = "1"
	args := []string{"--print", "--model", "fixture-model", "--tools", "Read", "--system-prompt", "Actual plugin startup consumer", "--thinking", "disabled", "/sample:sample"}
	command, _, err := ParseCommand(args, o.Version, o.WorkDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || requests.Load() != 1 || !strings.Contains(output.String(), "PUBLIC_SYNC_PLUGIN_FIRST_MODEL") {
		t.Fatal("public sync installation failed", code, err, requests.Load(), output.String(), string(body))
	}
	if !bytes.Contains(body, []byte("ACTUAL_NEW_MARKETPLACE_SKILL_FIRST")) {
		t.Fatal("first model missed actual materialized Skill", string(body))
	}
	known, err := os.ReadFile(filepath.Join(config, "plugins", "known_marketplaces.json"))
	if err != nil || obj(known).Get("mkt") == nil {
		t.Fatal("real marketplace producer did not persist", err, string(known))
	}
	settings, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	if !bytes.Equal(settings, initial) {
		t.Fatal("reconciliation changed declared user settings", string(settings))
	}
}

func TestActualPublicCLIHeadlessPluginSyncTimeoutAndCanceledInputJoinHeldHTTP(t *testing.T) {
	for _, phase := range []string{"timeout", "canceled-sync"} {
		t.Run(phase, func(t *testing.T) {
			started, joined := make(chan struct{}), make(chan struct{})
			market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(joined) }))
			defer market.Close()
			var requests atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The original CLI init HEAD is distinct from model POSTs.
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}

				select {
				case <-started:
				default:
					t.Error("ASK ran before actual sync install start")
				}
				requests.Add(1)
				_, _ = io.ReadAll(r.Body)
				response(w, "sync-timeout", "", nil, "PUBLIC_PLUGIN_TIMEOUT_MODEL")
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			settings := raw(map[string]any{"futureUser": map[string]bool{"keep": true}, "extraKnownMarketplaces": map[string]any{"held": map[string]any{"source": map[string]string{"source": "url", "url": market.URL}}}})
			_ = os.WriteFile(filepath.Join(config, "settings.json"), settings, 0600)
			for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
				environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"] = "1"
				if phase == "timeout" {
					environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL_TIMEOUT_MS"] = "100ms"
				}
			}
			command, _, err := ParseCommand([]string{"--print", "--model", "fixture-model", "--tools", "Read", "--system-prompt", "actual headless plugin timeout", "--thinking", "disabled", "ordinary human"}, o.Version, o.WorkDir, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			var output bytes.Buffer
			go func() { defer close(done); _, _ = RunCommand(ctx, command, o, strings.NewReader(""), &output, nil) }()
			deadline := time.NewTimer(8 * time.Second)
			defer deadline.Stop()
			select {
			case <-started:
			case <-deadline.C:
				t.Fatal("physical startup URL GET never admitted")
			}
			if phase == "canceled-sync" {
				cancel()
			}
			select {
			case <-done:
			case <-deadline.C:
				t.Fatal("public CLI failed to cancel/join owned sync installation")
			}
			select {
			case <-joined:
			case <-deadline.C:
				t.Fatal("physical marketplace connection remained after public CLI returned")
			}
			want := int32(1)
			if phase == "canceled-sync" {
				want = 0
			}
			if requests.Load() != want {
				t.Fatal("sync gate/timeout/cancel reached wrong model count", requests.Load(), output.String())
			}
			if phase == "timeout" && !strings.Contains(output.String(), "PUBLIC_PLUGIN_TIMEOUT_MODEL") {
				t.Fatal("timeout discarded actual ordinary model result", output.String())
			}
			after, _ := os.ReadFile(filepath.Join(config, "settings.json"))
			if !bytes.Equal(after, settings) {
				t.Fatal("failed/canceled startup changed declaration", string(after))
			}
		})
	}
}
