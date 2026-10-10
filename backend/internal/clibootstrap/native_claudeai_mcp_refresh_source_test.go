package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestClaudeMCPRefreshActualAuthSourceConcurrentForceScopeAndPhysicalHTTP(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, name := range []string{"expired", "fresh", "401-fresh", "401-expired", "401-replaced", "no-refresh", "not-inference", "null-expiry", "bare", "env-token", "refresh-503", "scope-not-saved", "same-access", "disk-replaced-after-warm", "401-disk-replaced-after-warm", "503-race-recovered", "missing-expiry", "profile-unavailable"} {
		t.Run(name, func(t *testing.T) {
			token := map[string]any{"accessToken": "fake-old", "refreshToken": "fake-refresh", "expiresAt": 17, "scopes": []string{"user:inference", "user:mcp_servers"}, "subscriptionType": "pro", "rateLimitTier": "owned-tier"}
			env := map[string]string{}
			action, sent := "check", "fake-old"
			switch name {
			case "fresh", "401-fresh":
				token["expiresAt"] = 9999999999999
			case "401-replaced":
				sent = "fake-previous"
			case "no-refresh":
				token["refreshToken"] = nil
			case "not-inference":
				token["scopes"] = []string{"user:mcp_servers"}
			case "null-expiry":
				token["expiresAt"] = nil
			case "missing-expiry":
				delete(token, "expiresAt")
			case "profile-unavailable":
				token["subscriptionType"], token["rateLimitTier"] = nil, nil
			case "bare":
				env["CLAUDE_CODE_SIMPLE"] = "1"
			case "env-token":
				env["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-env"
			}
			if strings.HasPrefix(name, "401-") {
				action = "401"
			}
			credentials := map[string]any{"claudeAiOauth": token, "future": map[string]any{"preserve": true}}
			global := map[string]any{"oauthAccount": map[string]any{"billingType": "owned", "accountCreatedAt": "owned", "subscriptionCreatedAt": "owned"}, "future": true}
			m := authCommandFixture(t, map[string]any{"env": env, "global": global})
			m.Native.NowMillis = func() float64 { return 1000000 }
			m.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
			config := m.Native.Environment["CLAUDE_CONFIG_DIR"]
			credentialPath := filepath.Join(config, ".credentials.json")
			agentsWrite(t, credentialPath, string(rawMain(credentials)))
			var replacement map[string]any
			if strings.Contains(name, "disk-replaced") || name == "503-race-recovered" {
				replacement = map[string]any{"future": map[string]any{"preserve": true}, "claudeAiOauth": map[string]any{"accessToken": "fake-disk-new", "refreshToken": "fake-disk-refresh", "expiresAt": 9999999999999, "scopes": []string{"user:inference", "user:mcp_servers"}, "subscriptionType": "pro", "rateLimitTier": "owned-tier"}}
			}
			var requests, profiles atomic.Int32
			var mu sync.Mutex
			bodies := []protocol.JSONObject{}
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/api/oauth/profile" {
					profiles.Add(1)
					if r.Header.Get("Authorization") != "Bearer fake-new" {
						t.Error("original profile token changed")
					}
					w.WriteHeader(403)
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/oauth/token" {
					t.Error("unexpected physical refresh path", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				requests.Add(1)
				data, _ := io.ReadAll(r.Body)
				var value protocol.JSONObject
				_ = value.UnmarshalJSON(data)
				mu.Lock()
				bodies = append(bodies, value)
				mu.Unlock()
				if value.String("refresh_token") != "fake-refresh" || value.String("scope") != "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload" {
					t.Error("original refresh scope/body changed", string(data))
				}
				if name == "refresh-503" || name == "503-race-recovered" {
					if replacement != nil {
						if err := os.WriteFile(credentialPath, rawMain(replacement), 0600); err != nil {
							t.Error(err)
						}
					}
					w.WriteHeader(503)
					return
				}
				// Keep the physical response in flight so all admitted parallel requests
				// encounter the same original pending promise instead of later cache hits.
				timer := time.NewTimer(100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
				access, scope := "fake-new", "user:inference user:mcp_servers"
				if name == "same-access" {
					access = "fake-old"
				}
				if name == "scope-not-saved" {
					scope = "user:mcp_servers"
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(rawMain(map[string]any{"access_token": access, "refresh_token": "fake-refresh-new", "expires_in": 3600, "scope": scope}))
			}))
			m.Native.Environment["HTTPS_PROXY"], m.Native.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
			binding, err := m.NewCLIClaudeAIMCP(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(name, "disk-replaced") {
				if _, _, err = binding.PrepareHTTP(context.Background(), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"claudeai-proxy"`)}, protocol.JSONField{Name: "id", Value: []byte(`"owned"`)}), mcpclient.Config{}); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(credentialPath, rawMain(replacement), 0600); err != nil {
					t.Fatal(err)
				}
			}
			concurrent := 6
			values := make([]bool, concurrent)
			failures := make(chan error, concurrent)
			var wait sync.WaitGroup
			for i := 0; i < concurrent; i++ {
				wait.Add(1)
				go func(i int) {
					defer wait.Done()
					var e error
					if action == "401" {
						values[i], e = binding.refresh.handle401(context.Background(), sent)
					} else {
						values[i], e = binding.refresh.flight(context.Background(), nil, func(ctx context.Context) (bool, error) { return binding.refresh.checkImpl(ctx, 0, false) })
					}
					if e != nil {
						failures <- e
					}
				}(i)
			}
			wait.Wait()
			close(failures)
			for e := range failures {
				t.Fatal(e)
			}
			binding.Close()
			goRequests, goProfiles := requests.Swap(0), profiles.Swap(0)
			goCredentialRaw, err := os.ReadFile(credentialPath)
			if err != nil {
				t.Fatal(err)
			}
			var goCredentials any
			_ = json.Unmarshal(goCredentialRaw, &goCredentials)
			path, _ := filepath.Abs("native_claudeai_mcp_refresh.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
			cmd.Dir = m.Native.Home
			cmd.Env = []string{"HOME=" + m.Native.Home, "USERPROFILE=" + m.Native.Home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + m.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(m.Native.Home), "CC_HAHA_CLAUDEAI_REFRESH_FIXTURE=1", "HTTPS_PROXY=" + proxy, "NODE_EXTRA_CA_CERTS=" + ca, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			input := map[string]any{"action": action, "sent": sent, "credentials": credentials, "global": global, "concurrent": concurrent}
			if strings.Contains(name, "disk-replaced") {
				input["replacement"] = replacement
			}
			cmd.Stdin = bytes.NewReader(rawMain(input))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal("actual Source auth", err, stderr.String())
			}
			var source struct {
				Values      []bool `json:"values"`
				Credentials any    `json:"credentials"`
			}
			if err = json.Unmarshal(out, &source); err != nil {
				t.Fatal(err, string(out))
			}
			if !reflect.DeepEqual(values, source.Values) || !reflect.DeepEqual(goCredentials, source.Credentials) || goRequests != requests.Load() || goProfiles != profiles.Load() {
				t.Fatalf("actual Source %s requests=%d\nGo values=%v credentials=%s requests=%d", out, requests.Load(), values, goCredentialRaw, goRequests)
			}
			mu.Lock()
			defer mu.Unlock()
			for i := 1; i < len(bodies); i++ {
				if !reflect.DeepEqual(bodies[0], bodies[i]) {
					t.Fatal("Source/Go physical request body changed")
				}
			}
		})
	}
}
