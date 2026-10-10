package clibootstrap

import (
	"context"
	"fmt"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func settingsShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func waitNativeSettings(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("actual MainPrint settings watcher did not settle")
}

func TestActualSettingsWatcherHookBlockThenDiskPermissionChangeSameHarness(t *testing.T) {
	var calls atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		switch n {
		case 1:
			emitNativeTaskTool(w, "settings-write-before", "Write", map[string]any{"file_path": target, "content": "before-settings"})
		case 2:
			emitNativeTaskTool(w, "settings-read-before", "Read", map[string]any{"file_path": target})
		case 3:
			if !strings.Contains(string(body), "before-settings") {
				t.Error("real initial Write/Read result not sent")
			}
			emitPlanFixture(w, 1, "", "FIRST_DONE")
		case 4:
			emitNativeTaskTool(w, "settings-write-denied", "Write", map[string]any{"file_path": target, "content": "must-not-replace"})
		case 5:
			var request protocol.JSONObject
			_ = request.UnmarshalJSON(body)
			denied := false
			for _, message := range taskBlocks(request.Get("messages")) {
				for _, block := range taskBlocks(message.Get("content")) {
					if block.String("tool_use_id") == "settings-write-denied" && string(block.Get("is_error")) == "true" {
						denied = true
					}
				}
			}
			if !denied {
				t.Error("new actual deny rule did not appear in next model POST")
			}
			emitPlanFixture(w, 1, "", "DENY_DONE")
		default:
			t.Error("unexpected model request", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	var config, allowed, hooksRun string
	var settings protocol.JSONObject
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		target = filepath.Join(n.CWD, "settings-result.txt")
		config = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
		allowed = filepath.Join(n.CWD, "allow-settings-change")
		hooksRun = filepath.Join(n.CWD, "settings-hook-runs")
		command := "printf 'called\\n' >> " + settingsShellQuote(hooksRun) + "; if test -f " + settingsShellQuote(allowed) + "; then printf '{}'; else printf '{\"decision\":\"block\",\"reason\":\"Block real settings update\"}'; exit 2; fi"
		settings = protocol.NewJSONObject()
		_ = settings.UnmarshalJSON(rawMain(map[string]any{"alwaysThinkingEnabled": false, "agentTeamsEnabled": false, "enabledPlugins": map[string]bool{"sample@mkt": false}, "hooks": map[string]any{"ConfigChange": []any{map[string]any{"matcher": "user_settings", "hooks": []any{map[string]any{"type": "command", "command": command}}}}}, "future": map[string]any{"keep": true}}))
		if err := os.WriteFile(config, rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(options *MainPrintOptions) {
		options.Arguments.PermissionMode = "default"
		options.Arguments.Tools = []string{"Read,Write"}
		options.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, _ queryloop.RegisteredTool, input *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			return queryloop.ToolPermission{Behavior: "allow", Input: input}, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("Actual initial Write and Read")); err != nil {
		t.Fatal(err)
	}
	next := settings.Clone()
	next.Set("permissions", []byte(`{"deny":["Write"]}`))
	if err := os.WriteFile(config, rawMain(next), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool { raw, _ := os.ReadFile(hooksRun); return strings.Count(string(raw), "called") >= 1 })
	var hasDeny bool
	readPermission := func() bool {
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			_, permission, err := m.Controller.SDKSettingsOnOwner(scope)
			if err != nil {
				return err
			}
			hasDeny = strings.Contains(string(permission.Get("alwaysDenyRules")), `"Write"`)
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		return hasDeny
	}
	if readPermission() {
		t.Fatal("blocking ConfigChange applied disk deny rule")
	}
	if err := os.WriteFile(allowed, []byte("allow"), 0600); err != nil {
		t.Fatal(err)
	}
	next.Set("future", []byte(`{"keep":true,"revision":2}`))
	if err := os.WriteFile(config, rawMain(next), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, readPermission)
	if err := m.RunTurn(ctx, user("Try actual Write after settings change")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "before-settings" {
		t.Fatal("new source deny did not protect actual file")
	}
	if calls.Load() != 5 {
		t.Fatal("settings watcher created a model request or second Harness")
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		actual, _, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if !strings.Contains(string(actual.Get("future")), `"revision":2`) {
			return fmt.Errorf("same AppState settings did not update unknown fields")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActualSettingsWatcherDisablesBypassAndChangesDefinedEffortOnly(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(options *MainPrintOptions) { options.Arguments.EffortLevel = "high" })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	if err := os.WriteFile(config, []byte(`{"enabledPlugins":{"sample@mkt":false},"permissions":{"disableBypassPermissionsMode":"disable"},"effortLevel":"low","future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	read := func() bool {
		ready := false
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			_, permission, err := m.Controller.SDKSettingsOnOwner(scope)
			if err != nil {
				return err
			}
			effort, _, err := m.Host.InferenceEffort(scope)
			if err != nil {
				return err
			}
			ready = permission.String("mode") == "default" && string(permission.Get("isBypassPermissionsModeAvailable")) == "false" && string(effort) == `"low"`
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return ready
	}
	waitNativeSettings(t, read)
	if err := os.WriteFile(config, []byte(`{"enabledPlugins":{"sample@mkt":false},"permissions":{},"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	waitNativeSettings(t, func() bool {
		settled := false
		if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			settings, permission, err := m.Controller.SDKSettingsOnOwner(scope)
			if err != nil {
				return err
			}
			effort, _, err := m.Host.InferenceEffort(scope)
			if err != nil {
				return err
			}
			settled = settings.Get("effortLevel") == nil && string(effort) == `"low"` && string(permission.Get("isBypassPermissionsModeAvailable")) == "false"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return settled
	})
}
