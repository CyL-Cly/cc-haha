package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func assertTerminalConfigRestoreLiteralJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	// Canonicalize independent literal JSON only; do not call the production
	// settings merge, permission patch builder, or Restore to compute an oracle.
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal("physical/snapshot JSON is invalid", err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal("literal fixture JSON is invalid", err)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("Escape changed the original user settings: got %s, want %s", actualJSON, expectedJSON)
	}
}

func TestTerminalConfigEscapeRestoresNestedDefaultModeAbsenceOnColdReopen(t *testing.T) {
	for _, row := range []struct {
		name, original string
	}{
		{
			name:     "permissions-exist-default-mode-absent",
			original: `{"enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["Read(owned/**)"],"deny":["Bash(rm *)"],"future":{"keep":["nested",7]}},"futureUser":{"keep":true}}`,
		},
		{
			name:     "explicit-default-mode-is-restored",
			original: `{"enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["Read(owned/**)"],"defaultMode":"acceptEdits","future":{"keep":["nested",7]}},"futureUser":{"keep":true}}`,
		},
		{
			name:     "whole-permissions-absent-is-restored",
			original: `{"enabledPlugins":{"sample@mkt":false},"futureUser":{"keep":true}}`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var requests atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "local Config fixture forbids model HTTP", http.StatusInternalServerError)
			}))
			t.Cleanup(peer.Close)
			// LIFO: the existing MainPrint and reopened session close before
			// the zero-HTTP assertion and the owned peer itself.
			t.Cleanup(func() {
				if count := requests.Load(); count != 0 {
					t.Errorf("local Config setting/restore made %d HTTP requests", count)
				}
			})
			var path string
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				path = filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
				if err := os.WriteFile(path, []byte(row.original), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(options *MainPrintOptions) { options.Interactive = true })
			ctx := context.Background()
			snapshot, err := m.TerminalConfigState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertTerminalConfigRestoreLiteralJSON(t, snapshot.Get("userSettings"), row.original)
			originalPermissions := nativeInProcessObject(nativeInProcessObject(snapshot.Get("userSettings")).Get("permissions"))
			originalDefaultMode := originalPermissions.Get("defaultMode")
			if err = m.SetTerminalConfigSetting(ctx, "defaultPermissionMode", []byte(`"plan"`)); err != nil {
				t.Fatal(err)
			}
			changed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode := nativeInProcessObject(nativeInProcessObject(changed).Get("permissions")).String("defaultMode"); mode != "plan" {
				t.Fatal("actual SetTerminalConfigSetting did not persist plan", mode)
			}
			// This is the public consumer used by Config's Escape callback.
			if err = m.RestoreTerminalConfig(ctx, snapshot, []string{"defaultPermissionMode"}); err != nil {
				t.Fatal(err)
			}
			restored, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertTerminalConfigRestoreLiteralJSON(t, restored, row.original)
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			// Reconstruct the actual MainPrint graph from the same private
			// files so an in-memory snapshot alone cannot satisfy the test.
			cold, err := NewMainPrint(m.options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := cold.ShutdownSession(context.Background()); err != nil {
					t.Error(err)
				}
			})
			if err = cold.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			coldState, err := cold.TerminalConfigState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertTerminalConfigRestoreLiteralJSON(t, coldState.Get("userSettings"), row.original)
			coldPermissions := nativeInProcessObject(nativeInProcessObject(coldState.Get("initial")).Get("permissions"))
			if mode := coldPermissions.Get("defaultMode"); !bytes.Equal(mode, originalDefaultMode) {
				t.Fatalf("cold merged settings retained a cancelled defaultMode: got %s, want %s", mode, originalDefaultMode)
			}
			physical, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertTerminalConfigRestoreLiteralJSON(t, physical, row.original)
		})
	}
}
