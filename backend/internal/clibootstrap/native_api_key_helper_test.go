package clibootstrap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainHelperCachesAcrossTurnsReloadsAnd401RefreshesSameHarness(t *testing.T) {
	var calls atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		want := "private-key-1"
		if n == 5 || n == 6 {
			want = "private-key-2"
		}
		if n >= 7 {
			want = "private-key-3"
		}
		if r.Header.Get("Authorization") != "Bearer "+want || r.Header.Get("x-api-key") != want {
			t.Error("actual helper credential did not reach original HTTP client", n, r.Header.Get("Authorization"), r.Header.Get("x-api-key"))
		}
		switch n {
		case 1:
			emitNativeTaskTool(w, "helper-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_HELPER_WRITE"})
		case 2:
			emitNativeTaskTool(w, "helper-read", "Read", map[string]any{"file_path": target})
		case 6:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"helper key expired"}}`)
		default:
			emitPlanFixture(w, 1, "", "HELPER_DONE")
		}
	}))
	defer peer.Close()
	var config, count, command string
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "ANTHROPIC_API_KEY")
		delete(n.Environment, "ANTHROPIC_AUTH_TOKEN")
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "1"
		config = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		target = filepath.Join(n.CWD, "helper-result.txt")
		count = filepath.Join(n.CWD, "helper-count")
		script := filepath.Join(n.CWD, "helper.py")
		code := fmt.Sprintf("from pathlib import Path\np=Path(%q)\nn=int(p.read_text())+1 if p.exists() else 1\np.write_text(str(n))\nprint('private-key-'+str(n))\n", count)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		command = "/usr/bin/python3 " + settingsShellQuote(script)
		settings := map[string]any{"apiKeyHelper": command, "alwaysThinkingEnabled": false, "agentTeamsEnabled": false, "enabledPlugins": map[string]bool{"sample@mkt": false}, "future": true}
		if err := os.WriteFile(config, rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write,Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	for _, prompt := range []string{"Actual helper Write/Read", "Same helper cache next human"} {
		if err := m.RunTurn(ctx, user(prompt)); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := os.ReadFile(count); err != nil || string(raw) != "1" {
		t.Fatal("cold helper did not cache across actual turns", string(raw), err)
	}
	changed := command + " "
	settings := map[string]any{"apiKeyHelper": changed, "alwaysThinkingEnabled": false, "agentTeamsEnabled": false, "enabledPlugins": map[string]bool{"sample@mkt": false}, "future": map[string]any{"revision": 2}}
	if err := os.WriteFile(config, rawMain(settings), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		ready := false
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			actual, _, err := m.Controller.SDKSettingsOnOwner(scope)
			if err == nil {
				ready = actual.String("apiKeyHelper") == changed && m.apiKeyHelper.Cached() == nil
			}
			return err
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		return ready
	})
	if err := m.RunTurn(ctx, user("Actual changed helper next human")); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, user("Actual 401 invalidates and reruns helper")); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, user("Next human after original api retry notice")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 8 {
		t.Fatal("extra or missing original model request", calls.Load())
	}
	if raw, err := os.ReadFile(count); err != nil || string(raw) != "3" {
		t.Fatal("settings/401 did not invalidate real helper cache", string(raw), err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "ACTUAL_HELPER_WRITE" {
		t.Fatal("actual shared Write/Read produced no physical result", string(raw), err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		source, err := m.nativeAPIKeySource(scope)
		if err == nil && source != "apiKeyHelper" {
			return fmt.Errorf("original helper source changed: %s", source)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHelperConfiguredProjectTrustAndBareFlagSource(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, nil)
	ctx := context.Background()
	for _, raw := range []string{`{"apiKeyHelper":"printf project"}`, `{"apiKeyHelper":"printf project\ud800"}`, `{"apiKeyHelper":"printf \u0070roject"}`} {
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			settings := filepath.Join(m.options.Native.CWD, ".claude", "settings.json")
			if err := os.WriteFile(settings, []byte(raw), 0600); err != nil {
				return err
			}
			m.settingState.ResetSettingsCache()
			m.apiKeyHelper.Clear()
			r, err := m.nativeAPIKeyHelperRequest(scope, false)
			if err != nil {
				return err
			}
			if !r.ProjectOrLocal || r.TrustAccepted || r.NonInteractive {
				return fmt.Errorf("untrusted project helper admitted: %+v", r)
			}
			lookup, err := m.apiKeyHelper.Begin(r)
			if err != nil {
				return err
			}
			future, err := nativeSkillWork(m, scope, func() (*string, error) { return lookup.Wait(ctx) })
			if err != nil {
				return err
			}
			value, err := future.Await(scope)
			if err != nil || value != nil {
				return fmt.Errorf("untrusted project credential command executed %v", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
