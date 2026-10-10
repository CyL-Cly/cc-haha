package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestIssuedReleaseReplBridgeRejectsDefaultExternalCachedAndDevOverrides(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("explicit pinned actual Source required")
	}
	for _, name := range []string{"issued-transcript-only-build", "external-enabled-build-cached-entitlement", "external-enabled-build-ant-only-dev-overrides", "external-console-key"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "no released Bridge grant", 403)
			}))
			defer peer.Close()
			home := t.TempDir()
			if e := os.Mkdir(filepath.Join(home, "tmp"), 0700); e != nil {
				t.Fatal(e)
			}
			enabled := name != "issued-transcript-only-build"
			secure := any(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "owned-Source-bridge-oauth", "scopes": []string{"user:profile", "user:inference"}, "expiresAt": 2000000000000.0, "subscriptionType": "max"}})
			if name == "external-console-key" {
				secure = nil
			}
			global := map[string]any{"cachedGrowthBookFeatures": map[string]any{"tengu_ccr_bridge": true}, "growthBookOverrides": map[string]any{"tengu_ccr_bridge": true}}
			input, _ := json.Marshal(map[string]any{"home": home, "origin": peer.URL, "expectedPrompts": 0, "bridgeBuildEnabled": enabled, "secure": secure, "global": global})
			fixture, _ := filepath.Abs("../../testdata/repl-bridge-physical.fixture.ts")
			profile, _ := filepath.Abs("../../testdata/loopback-only.sb")
			args := []string{"-f", profile, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER"}
			if enabled {
				args = append(args, "--feature=BRIDGE_MODE")
			}
			args = append(args, fixture)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", args...)
			cmd.Dir = home
			cmd.Stdin = bytes.NewReader(input)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "CC_HAHA_REPL_BRIDGE_FIXTURE=1", "ANTHROPIC_BASE_URL=" + peer.URL, "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-6", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_ENABLE_CFC=0", "CLAUDE_CODE_SYNTAX_HIGHLIGHT=0"}
			if name == "external-enabled-build-ant-only-dev-overrides" {
				cmd.Env = append(cmd.Env, "CLAUDE_BRIDGE_OAUTH_TOKEN=owned-Source-bridge-oauth", "CLAUDE_BRIDGE_BASE_URL="+peer.URL, "CLAUDE_BRIDGE_SESSION_INGRESS_URL="+peer.URL, `CLAUDE_INTERNAL_FC_OVERRIDES={"tengu_ccr_bridge":true}`)
			}
			if name == "external-console-key" {
				cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY=owned-console-key")
			}
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("actual Source released/external refusal %v %s", e, out)
			}
			var got struct {
				Rows               []any
				WSOpens            []any
				HandlePresent      bool
				BridgeBuildEnabled bool
				SessionOverride    *string
				Blocked            []string
			}
			if e = json.Unmarshal(out, &got); e != nil {
				t.Fatal(e, string(out))
			}
			if len(got.Rows) != 0 || len(got.WSOpens) != 0 || got.HandlePresent || got.SessionOverride != nil || len(got.Blocked) != 0 || got.BridgeBuildEnabled != enabled || calls.Load() != 0 {
				t.Fatal("Source admitted absent Bridge grant", string(out), calls.Load())
			}
			f := newNativePluginFixture(t)
			f.n.Environment["USER_TYPE"] = "external"
			f.n.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.n.Environment["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
			f.n.Environment["CLAUDE_BRIDGE_OAUTH_TOKEN"] = "owned-Go-bridge-oauth"
			f.n.Environment["CLAUDE_BRIDGE_BASE_URL"] = peer.URL
			f.n.Environment["CLAUDE_BRIDGE_SESSION_INGRESS_URL"] = peer.URL
			f.n.Environment["CLAUDE_INTERNAL_FC_OVERRIDES"] = `{"tengu_ccr_bridge":true}`
			f.write(t, filepath.Join(f.config, "settings.json"), `{"model":"opus","future":true}`)
			f.write(t, filepath.Join(f.config, ".claude.json"), `{"remoteControlAtStartup":true,"cachedGrowthBookFeatures":{"tengu_ccr_bridge":true},"growthBookOverrides":{"tengu_ccr_bridge":true},"future":true}`)
			f.n.RunSecurity = func(_ context.Context, _ securestorage.Command) ([]byte, error) {
				if name == "external-console-key" {
					return []byte(`{}`), nil
				}
				return []byte(`{"claudeAiOauth":{"accessToken":"owned-Go-bridge-oauth","scopes":["user:profile","user:inference"],"expiresAt":2000000000000,"subscriptionType":"max"}}`), nil
			}
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Model: "opus", Tools: []string{"Read"}, MaxTurns: 8, PermissionMode: "default", SessionID: "6d3e6d33-73fb-4715-a6fc-17452e0cff4d", SystemPrompt: "owned absent Bridge grant"}, Native: f.n, PID: os.Getpid(), Interactive: true, Platform: "darwin", Version: "999.0.0-fixture", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				state, e := m.Controller.TypedModelStateOnOwner(scope)
				if e != nil {
					return e
				}
				if state.ForSession != nil || state.Base == nil || *state.Base != "opus" {
					t.Fatal("absent Bridge changed raw model", state)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if calls.Load() != 0 {
				t.Fatal("Go default/external issued remote Bridge HTTP", calls.Load())
			}
		})
	}
}
