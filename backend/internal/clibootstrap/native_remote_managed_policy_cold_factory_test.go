package clibootstrap

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// This top uses only pre-existing public factories, so exact-old production
// compiles and its cold-cache capability is a real constructor counterexample.
func TestRemoteManagedPolicyColdActualMainPrintAndMainCommand(t *testing.T) {
	for _, kind := range []string{"print", "command"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.URL.Path != "/api/claude_code/settings" || r.Header.Get("Authorization") != "Bearer fake-owned-env" {
					t.Error("cold Source escaped the owned settings contract", r.Method, r.URL.Path)
					w.WriteHeader(403)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"model":"opus"}}`)
			}))
			raw := managedSourceJSON(t, map[string]string{"HTTPS_PROXY": proxy, "NODE_EXTRA_CA_CERTS": ca}, map[string]any{"fallback": map[string]any{"model": "haiku"}})
			var actual, want any
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err, string(raw))
			}
			if err := json.Unmarshal([]byte(`{"eligible":true,"before":null,"after":{"model":"opus"},"policy":{"model":"opus"},"changes":["policySettings"],"checksum":null,"disk":{"model":"opus"},"mode":384}`), &want); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 || !reflect.DeepEqual(actual, want) {
				t.Fatal("cold actual Source was not qualified", requests.Load(), string(raw))
			}
			t.Logf("REMOTE_MANAGED_COLD_SOURCE kind=%s json=%s", kind, raw)
			if kind == "command" {
				m := authCommandFixture(t, map[string]any{"env": map[string]string{"CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "0", "CLAUDE_CODE_OAUTH_TOKEN": "fake-owned-env"}, "nonInteractive": true})
				if _, err := m.Startup.GetInitialSettings(); err != nil {
					t.Fatal("actual command cold policy init", err)
				}
				return
			}
			f := newNativePluginFixture(t)
			delete(f.n.Environment, "ANTHROPIC_BASE_URL")
			f.n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-owned-env"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			m, err := NewMainPrint(MainPrintOptions{Native: f.n, Platform: "darwin", PID: os.Getpid(), Version: "2.1.288", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", Sources: DefaultState(), SystemPrompt: "private remote managed policy", Tools: []string{""}, MaxTurns: 1}})
			if err != nil {
				t.Fatal("actual print cold policy init", err)
			}
			defer m.Close()
			if _, err = m.Startup.GetInitialSettings(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
