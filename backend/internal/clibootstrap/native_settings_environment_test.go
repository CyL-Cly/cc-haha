package clibootstrap

import (
	"context"
	"encoding/json"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSettingsEnvChangesNextRequestBashHookAndSharedCACache(t *testing.T) {
	var firstCalls, nextCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		emitPlanFixture(w, 1, "", "FIRST_DONE")
	}))
	defer first.Close()
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := nextCalls.Add(1)
		if r.Header.Get("x-settings-revision") != "updated" {
			t.Error("next real HTTP request retained old headers")
		}
		switch n {
		case 1, 3:
			emitNativeTaskTool(w, "settings-env-bash-"+string(rune('0'+n)), "Bash", map[string]any{"command": "printf '%s|%s' \"$PROBE_CHANGE\" \"$PROBE_RETAIN\"", "description": "Read actual changed environment"})
		case 2, 4:
			if !strings.Contains(string(body), "new|retained") {
				t.Error("physical Bash did not read additive updated environment")
			}
			emitPlanFixture(w, 1, "", "ENV_DONE")
		default:
			t.Error("extra model request", n)
			http.Error(w, "extra", 500)
		}
	}))
	defer next.Close()
	var config, hookPath, oldCA, newCA string
	m := backgroundAgentMainWithOptions(t, first.URL, func(n *NativeBoundary) {
		config = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		hookPath = filepath.Join(n.CWD, "env-hook-runs")
		oldCA = filepath.Join(n.CWD, "old-ca.txt")
		newCA = filepath.Join(n.CWD, "new-ca.txt")
		n.Environment["NODE_EXTRA_CA_CERTS"] = oldCA
		if err := os.WriteFile(oldCA, []byte("old-owned-cert"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newCA, []byte("new-owned-cert"), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Bash"} })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	read := func(callback func(*cooperative.Scope) error) error {
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, callback)
		if err != nil {
			return err
		}
		return receipt.Wait()
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("First actual request")); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := m.tls.Certificates(m.nativeOpenAIEnvironment())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	settings := protocol.NewJSONObject()
	_ = settings.UnmarshalJSON([]byte(`{"alwaysThinkingEnabled":false,"agentTeamsEnabled":false,"enabledPlugins":{"sample@mkt":false}}`))
	hook := map[string]any{"ConfigChange": []any{map[string]any{"matcher": "user_settings", "hooks": []any{map[string]any{"type": "command", "command": "printf '%s\\n' \"${PROBE_CHANGE-unset}\" >> " + settingsShellQuote(hookPath)}}}}}
	settings.Set("hooks", rawMain(hook))
	settings.Set("env", rawMain(map[string]string{"PROBE_CHANGE": "new", "PROBE_RETAIN": "retained", "ANTHROPIC_BASE_URL": next.URL, "ANTHROPIC_CUSTOM_HEADERS": "x-settings-revision: updated", "NODE_EXTRA_CA_CERTS": newCA}))
	if err := os.WriteFile(config, rawMain(settings), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		ready := false
		if err := read(func(scope *cooperative.Scope) error {
			ready = m.nativeOpenAIEnvironment()["ANTHROPIC_BASE_URL"] == next.URL
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return ready
	})
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		certs, err := m.tls.Certificates(m.nativeOpenAIEnvironment())
		if err != nil {
			return err
		}
		var text string
		_ = json.Unmarshal(certs.Items[len(certs.Items)-1], &text)
		if text != "new-owned-cert" {
			t.Error("same TLS cache retained original physical CA file")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, user("Actual Bash with updated env")); err != nil {
		t.Fatal(err)
	}
	settings.Set("env", []byte(`{}`))
	settings.Set("future", []byte(`{"revision":2}`))
	if err := os.WriteFile(config, rawMain(settings), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		ready := false
		if err := read(func(scope *cooperative.Scope) error {
			state, _, err := m.Controller.SDKSettingsOnOwner(scope)
			if err != nil {
				return err
			}
			ready = state.Get("future") != nil
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return ready
	})
	if err := m.RunTurn(ctx, user("Actual Bash after removing settings keys")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(hookPath)
	if err != nil || string(raw) != "new\n" {
		t.Fatalf("next actual ConfigChange hook did not inherit updated process env %q %v", raw, err)
	}
	if firstCalls.Load() != 1 || nextCalls.Load() != 4 {
		t.Fatal("settings side effect created another Harness or request", firstCalls.Load(), nextCalls.Load())
	}
}
