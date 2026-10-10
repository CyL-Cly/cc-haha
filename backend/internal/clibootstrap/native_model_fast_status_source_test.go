package clibootstrap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeModelFastStatusActualSourceAxiosAuthConcurrentMemoCacheAndLateClose(t *testing.T) {
	for _, name := range []string{"enable", "disable", "same-enabled", "http-failure-positive-cache", "oauth-profile", "oauth-no-profile", "oauth401-unexpired", "oauth401-expired", "revoked403-expired", "stale-growthbook-cache"} {
		t.Run(name, func(t *testing.T) {
			type record struct {
				Path, Authorization, Key, Beta, Agent string
				Body                                  json.RawMessage
			}
			var mu sync.Mutex
			seen := map[string][]record{}
			release := make(chan struct{})
			entered := make(chan struct{}, 1)
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				client := "Go"
				if r.Header.Get("x-api-key") == "owned-Source-fast-key" || strings.Contains(r.Header.Get("Authorization"), "Source") || bytes.Contains(b, []byte("owned-Source")) {
					client = "Source"
				}
				mu.Lock()
				seen[client] = append(seen[client], record{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key"), r.Header.Get("anthropic-beta"), r.Header.Get("User-Agent"), b})
				index := len(seen[client])
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/oauth/token" {
					_, _ = w.Write([]byte(`{"access_token":"owned-` + client + `-fresh","refresh_token":"owned-fake-refresh","expires_in":3600,"scope":"user:profile user:inference"}`))
					return
				}
				if r.URL.Path == "/api/oauth/profile" {
					_, _ = w.Write([]byte(`{"account":{},"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_5x"}}`))
					return
				}
				if r.URL.Path != "/api/claude_code_penguin_mode" {
					t.Errorf("unexpected fast HTTP %s", r.URL)
					http.Error(w, "unexpected", 400)
					return
				}
				if client == "Go" && index == 1 {
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if name == "http-failure-positive-cache" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":"owned status failure"}`))
					return
				}
				if strings.Contains(name, "401") && index == 1 {
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":"owned auth failure"}`))
					return
				}
				if strings.HasPrefix(name, "revoked403") && index == 1 {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`"OAuth token has been revoked"`))
					return
				}
				enabled := name != "disable"
				_, _ = w.Write([]byte(string(rawMain(map[string]any{"enabled": enabled, "disabled_reason": "preference"}))))
			}))
			defer peer.Close()
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
			global := map[string]any{"future": true}
			if name == "same-enabled" || name == "http-failure-positive-cache" || name == "disable" {
				global["penguinModeOrgEnabled"] = true
			}
			if name == "stale-growthbook-cache" {
				global["cachedGrowthBookFeatures"] = map[string]any{"tengu_miraculo_the_bard": true, "tengu_cicada_nap_ms": 30000}
				global["startupPrefetchedAt"] = float64(time.Now().UnixMilli())
			}
			settings := map[string]any{"model": "opus", "fastMode": true, "future": true}
			oauth := strings.HasPrefix(name, "oauth") || strings.HasPrefix(name, "revoked")
			secureFor := func(client string) map[string]any {
				scopes := []string{"user:profile", "user:inference"}
				if name == "oauth-no-profile" {
					scopes = []string{"user:inference"}
				}
				expires := float64(2000000000000)
				if strings.Contains(name, "expired") && !strings.Contains(name, "unexpired") {
					expires = 1000
				}
				return map[string]any{"claudeAiOauth": map[string]any{"accessToken": "owned-" + client + "-token", "refreshToken": "owned-" + client + "-refresh", "expiresAt": expires, "scopes": scopes, "subscriptionType": "max", "rateLimitTier": "default_claude_max_5x"}, "future": true}
			}
			var sourceSecure any
			if oauth {
				sourceSecure = secureFor("Source")
			}
			source := runNativeModelFastStatusSource(t, peer.URL, cert, settings, global, sourceSecure)
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
			f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
			certPath := filepath.Join(f.home, "root.pem")
			f.write(t, certPath, string(cert))
			env := f.n.Environment
			env["ANTHROPIC_API_KEY"] = "owned-Go-fast-key"
			env["NODE_ENV"] = "test"
			env["NODE_EXTRA_CA_CERTS"] = certPath
			env["USER_TYPE"] = "external"
			env["USE_LOCAL_OAUTH"] = "1"
			env["CLAUDE_LOCAL_OAUTH_API_BASE"] = peer.URL
			env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
			env["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
			var secure map[string]any
			if oauth {
				secure = secureFor("Go")
			}
			f.n.RunSecurity = func(_ context.Context, c securestorage.Command) ([]byte, error) {
				if len(c.Args) > 0 && c.Args[0] == "find-generic-password" {
					return json.Marshal(secure)
				}
				if c.Input != "" {
					parts := strings.Split(c.Input, `-X "`)
					if len(parts) != 2 {
						t.Fatal("missing original secure write hex", c.Args)
					}
					encoded := strings.Split(parts[1], `"`)[0]
					decoded, e := hex.DecodeString(encoded)
					if e != nil {
						t.Fatal(e)
					}
					if e = json.Unmarshal(decoded, &secure); e != nil {
						t.Fatal(e)
					}
				}
				return nil, nil
			}
			var notices []protocol.JSONObject
			config, _ := desktopoauth.ResolveConfig(env)
			config.BaseAPIURL = peer.URL
			config.TokenURL = peer.URL + "/v1/oauth/token"
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Model: "opus", Tools: []string{"Read"}, MaxTurns: 8, PermissionMode: "default", SessionID: "6d3e6d33-73fb-4715-a6fc-17452e0cff4d", SystemPrompt: "owned fast Source consumer"}, Native: f.n, NativeModelFastConfig: &config, PID: os.Getpid(), Interactive: true, Platform: "darwin", Version: "999.0.0-fixture", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnNativeFastModeNotification: func(value protocol.JSONObject) error { notices = append(notices, value.Clone()); return nil }})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			if name != "oauth-no-profile" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					mu.Lock()
					records := append([]record{}, seen["Go"]...)
					mu.Unlock()
					t.Fatal("background kickoff did not reach held real HTTP", records)
				}
			}
			// Startup has returned while the actual status request is still held.
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				state, e := m.Controller.TypedModelStateOnOwner(scope)
				if e == nil && state.FastMode != source.Before.Initial {
					t.Errorf("original pending initial %v Go %v", source.Before.Initial, state.FastMode)
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			close(release)
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				done := m.modelCommandRuntime.statusDone
				if done != nil {
					return scope.AwaitPromise(done)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				runtime := m.modelCommandRuntime
				if runtime.orgEnabled != source.Available {
					t.Errorf("Source availability %v Go %v reason %s", source.Available, runtime.orgEnabled, runtime.orgReason)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			saved, e := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if e != nil {
				t.Fatal(e)
			}
			var got map[string]any
			_ = json.Unmarshal(saved, &got)
			if !reflect.DeepEqual(got, source.Settings) {
				t.Fatal("Source settings", got, source.Settings)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				g, e := m.Global.Get()
				if e == nil && !reflect.DeepEqual(anyJSON(g.Get("penguinModeOrgEnabled")), source.Global["penguinModeOrgEnabled"]) {
					t.Errorf("Source global fast cache %s want %v", g.Get("penguinModeOrgEnabled"), source.Global["penguinModeOrgEnabled"])
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			recordsSource, recordsGo := seen["Source"], seen["Go"]
			mu.Unlock()
			if len(recordsSource) != len(recordsGo) {
				t.Fatal("Source concurrent prefetch/refresh HTTP counts", recordsSource, recordsGo)
			}
			for i, w := range recordsSource {
				g := recordsGo[i]
				if g.Path != w.Path || g.Beta != w.Beta || g.Agent != w.Agent || strings.ReplaceAll(g.Authorization, "Go", "Source") != w.Authorization || strings.ReplaceAll(g.Key, "Go", "Source") != w.Key {
					t.Fatal("actual Source Axios auth wire", i, g, w)
				}
			}
			_ = notices
		})
	}
}

type nativeFastSourceResult struct {
	Before           struct{ Available, Initial bool }
	Available        bool
	Reason           *string
	Signals          []bool
	Global, Settings map[string]any
	Secure           any
}

func anyJSON(raw json.RawMessage) any { var value any; _ = json.Unmarshal(raw, &value); return value }
func runNativeModelFastStatusSource(t *testing.T, origin string, certificate []byte, settings, global map[string]any, secure any) nativeFastSourceResult {
	t.Helper()
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("explicitly pinned Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.WriteFile(filepath.Join(home, "root.pem"), certificate, 0600)
	fixture, e := filepath.Abs("../../testdata/native-model-fast-status.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	profile, e := filepath.Abs("../../testdata/loopback-only.sb")
	if e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"home": home, "origin": origin, "certificate": string(certificate), "settings": settings, "global": global, "secure": secure})
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MODEL_FAST_STATUS_FIXTURE=1", "NODE_EXTRA_CA_CERTS=" + filepath.Join(home, "root.pem"), "NODE_ENV=production", "USER_TYPE=external", "USE_LOCAL_OAUTH=1", "CLAUDE_LOCAL_OAUTH_API_BASE=" + origin, "ANTHROPIC_API_KEY=owned-Source-fast-key", "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-6", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "DISABLE_TELEMETRY=1"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal("actual stock Axios/fast Source", e, string(out))
	}
	var result nativeFastSourceResult
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out))
	}
	return result
}
