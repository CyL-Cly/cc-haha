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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The reference executes whole main/Commander, actual cached authentication,
// migrations and the original exporter. It does not pass prepared Source
// metadata to Go. The compared fields are read from both real socket bodies.
func TestNativeCommandMigrationsFirstPartyModelEpochAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun not configured")
	}
	_, caller, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(caller), "../../../scripts/quality-gate/go-parity/native-command-migrations.fixture.ts")
	for _, kind := range []string{"sonnet-pro", "sonnet-max-from-1m", "opus-max", "opus-pro", "opus-unknown", "bypass-managed", "bypass-project-only", "mcp-git", "mcp-git-privacy", "changelog-new", "changelog-existing", "changelog-write-failure", "already-v11"} {
		t.Run(kind, func(t *testing.T) {
			var lock sync.Mutex
			bodies := [][]byte{}
			headers := []http.Header{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/events" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				lock.Lock()
				bodies = append(bodies, body)
				headers = append(headers, r.Header.Clone())
				lock.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer peer.Close()
			owned := t.TempDir()
			home := filepath.Join(owned, "source")
			if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]any{"home": home, "kind": kind, "origin": peer.URL})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			child.Dir, child.Stdin = home, bytes.NewReader(input)
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + owned, "CC_HAHA_COMMAND_MIGRATIONS_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_BASE_URL=" + peer.URL + "/launch", "NO_COLOR=1"}
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Run(); err != nil || stdout.String() != "[]\n" || stderr.Len() != 0 {
				t.Fatal("whole Source ordinary migration/exporter", err, stdout.String(), stderr.String())
			}
			sourceReceipt, err := os.ReadFile(filepath.Join(home, "command-migrations-source.json"))
			if err != nil {
				t.Fatal(err)
			}
			var identity struct{ ModuleIdentity map[string]bool }
			if err := json.Unmarshal(sourceReceipt, &identity); err != nil || len(identity.ModuleIdentity) != 4 {
				t.Fatal("whole Source alias/module identity missing", err)
			}
			for name, same := range identity.ModuleIdentity {
				if !same {
					t.Fatal("whole Source resolved a foreign module instance", name)
				}
			}
			lock.Lock()
			sourceBodies := append([][]byte(nil), bodies...)
			sourceHeaders := append([]http.Header(nil), headers...)
			bodies, headers = nil, nil
			lock.Unlock()
			profile := kind
			if kind == "mcp-git-privacy" {
				profile = "mcp-git"
			}
			f := nativeCommandMigrationFixture(t, profile)
			if kind != "mcp-git-privacy" {
				delete(f.n.Environment, "DISABLE_TELEMETRY")
			}
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL + "/safe"
			m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "999.0.0-command-sinks-auth", NonInteractive: true, Output: io.Discard, Error: io.Discard,
				AnalyticsHTTP: func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
					if input.URL != "https://api.anthropic.com/api/event_logging/batch" || input.Timeout != 10*time.Second {
						return analytics.Response{}, fmt.Errorf("foreign actual exporter target %s", input.URL)
					}
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/events", bytes.NewReader(input.Body))
					if err != nil {
						return analytics.Response{}, err
					}
					request.Header = input.Headers.Clone()
					response, err := peer.Client().Do(request)
					if err != nil {
						return analytics.Response{}, err
					}
					defer response.Body.Close()
					_, err = io.Copy(io.Discard, response.Body)
					return analytics.Response{Status: response.StatusCode}, err
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Run(ctx, func(scope *cooperative.Scope) error {
				result, err := m.PluginCommand(ctx, scope, plugincli.Command{Action: "list", JSON: true})
				if err != nil {
					return err
				}
				if result.ExitCode != 0 || result.Stderr != "" || strings.TrimSpace(result.Stdout) != "[]" {
					return fmt.Errorf("actual plugin handler differs: %+v", result)
				}
				// This observes the real graceful exporter boundary, like the
				// Source fixture. It does not change ordinary process.exit.
				return m.commandAnalytics.Flush(scope)
			}); err != nil {
				t.Fatal(err)
			}
			m.Close()
			lock.Lock()
			nativeBodies := append([][]byte(nil), bodies...)
			nativeHeaders := append([]http.Header(nil), headers...)
			lock.Unlock()
			if receipts := os.Getenv("CC_HAHA_MIGRATION_EPOCH_RECEIPTS"); receipts != "" {
				if !filepath.IsAbs(receipts) {
					t.Fatal("epoch receipt directory must be absolute")
				}
				raw, err := json.MarshalIndent(struct {
					SourceBodies  [][]byte
					SourceHeaders []http.Header
					NativeBodies  [][]byte
					NativeHeaders []http.Header
					SourceReceipt json.RawMessage
				}{sourceBodies, sourceHeaders, nativeBodies, nativeHeaders, sourceReceipt}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(receipts, kind+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if len(sourceBodies) != len(nativeBodies) {
				t.Fatal("actual exporter request count", len(sourceBodies), len(nativeBodies))
			}
			for index := range sourceBodies {
				source, native := migrationWireEpoch(t, sourceBodies[index]), migrationWireEpoch(t, nativeBodies[index])
				if !sameMigrationWireEpoch(source, native) {
					t.Fatalf("actual migration model/betas sampling differs\nSource=%s\nNative=%s", mustMigrationJSON(source), mustMigrationJSON(native))
				}
				for _, name := range []string{"authorization", "anthropic-beta", "x-api-key"} {
					if sourceHeaders[index].Get(name) != nativeHeaders[index].Get(name) {
						t.Fatal("actual cached/private auth producer differs", name, sourceHeaders[index].Get(name), nativeHeaders[index].Get(name))
					}
				}
			}
		})
	}
}

func migrationWireEpoch(t *testing.T, raw []byte) protocol.JSONObject {
	t.Helper()
	var body struct {
		Events []protocol.JSONObject `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err, string(raw))
	}
	rows := []json.RawMessage{}
	for _, event := range body.Events {
		var data protocol.JSONObject
		if err := data.UnmarshalJSON(event.Get("event_data")); err != nil {
			t.Fatal(err)
		}
		row := protocol.NewJSONObject()
		for _, key := range []string{"event_name", "model", "betas", "additional_metadata"} {
			if value := data.Get(key); value != nil {
				row.Set(key, value)
			}
		}
		encoded, _ := row.MarshalJSON()
		rows = append(rows, encoded)
	}
	encoded, _ := json.Marshal(rows)
	return protocol.NewJSONObject(protocol.JSONField{Name: "events", Value: encoded})
}

// Independent Source metadata I/O can complete out of public logEvent order.
// The epoch contract compares every complete event projection with multiplicity;
// it does not assign an event's model/betas by its eventual batch array position.
func sameMigrationWireEpoch(source, native protocol.JSONObject) bool {
	var sourceRows, nativeRows []json.RawMessage
	if json.Unmarshal(source.Get("events"), &sourceRows) != nil || json.Unmarshal(native.Get("events"), &nativeRows) != nil || len(sourceRows) != len(nativeRows) {
		return false
	}
	counts := make(map[string]int, len(sourceRows))
	for _, row := range sourceRows {
		counts[string(row)]++
	}
	for _, row := range nativeRows {
		key := string(row)
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}
