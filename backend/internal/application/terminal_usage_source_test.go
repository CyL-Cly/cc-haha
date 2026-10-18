package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualSourceUsageSubscriberProfileExpiryHeadersAndPhysicalTLS(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	for _, name := range []string{"subscriber", "no-profile", "no-inference", "expired", "null-expiry", "external-key", "response-error"} {
		t.Run(name, func(t *testing.T) {
			body := raw(map[string]any{"five_hour": map[string]any{"utilization": 23.9, "resets_at": "2026-10-14T05:00:00Z"}, "seven_day": map[string]any{"utilization": 63.0, "resets_at": "2026-10-19T00:00:00Z"}, "future": map[string]any{"retain": 17}})
			status := 200
			if name == "response-error" {
				status = 503
				body = raw(map[string]any{"error": "owned-limit-unavailable"})
			}
			var goCalls, sourceCalls atomic.Int32
			serve := func(calls *atomic.Int32) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.URL.Path != "/api/oauth/usage" || r.Header.Get("Authorization") != "Bearer fake-usage-token" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" || r.Header.Get("User-Agent") != "claude-code/2.1.288" || r.Header.Get("Content-Type") != "application/json" {
						t.Error("actual wire contract", r.Method, r.URL, r.Header)
					}
					w.WriteHeader(status)
					_, _ = w.Write(body)
				})
			}
			proxy, ca := terminalUsagePrivateTLS(t, serve(&goCalls))
			sourcePeer := httptest.NewServer(serve(&sourceCalls))
			defer sourcePeer.Close()
			o := nativeSessionOptions(t, "http://127.0.0.1:9")
			o.Interactive = true
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				delete(env, "ANTHROPIC_API_KEY")
				env["HTTPS_PROXY"] = proxy
				env["NODE_EXTRA_CA_CERTS"] = ca
				env["USER_TYPE"] = "external"
			}
			if name == "external-key" {
				for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
					env["ANTHROPIC_API_KEY"] = "fake-only-loopback"
					env["NODE_ENV"] = "test"
				}
			}
			home := o.Environment["HOME"]
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			scopes := []string{"user:inference", "user:profile"}
			if name == "no-profile" {
				scopes = []string{"user:inference"}
			}
			if name == "no-inference" {
				scopes = []string{"user:profile"}
			}
			var expires any = float64(9999999999999)
			if name == "expired" {
				expires = 17
			}
			if name == "null-expiry" {
				expires = nil
			}
			credentials := raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-usage-token", "refreshToken": nil, "scopes": scopes, "expiresAt": expires, "subscriptionType": "pro"}, "future": map[string]any{"preserve": 17}})
			if err := os.WriteFile(filepath.Join(config, ".credentials.json"), credentials, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]any{"billingType": "stripe_subscription"}, "future": 17}), 0600); err != nil {
				t.Fatal(err)
			}
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
			o.Native.NowMillis = func() float64 { return float64(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC).UnixMilli()) }
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			got, goErr := s.process.TerminalUsage(context.Background())
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-usage.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "TZ=UTC", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_USAGE_FIXTURE=1"}
			if name == "external-key" {
				cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY=fake-only-loopback", "NODE_ENV=test")
			}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "peer": sourcePeer.URL, "now": "2026-10-14T00:00:00Z"}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				Utilization json.RawMessage
				Error       *struct {
					Message string
					Body    json.RawMessage
				}
				Requests []struct {
					URL     string
					Method  string
					Timeout int
				}
				Subscriber, HasProfile, ExtraEnabled bool
				Registration                         struct {
					Name, Kind   string
					Availability []string
				}
				Caps struct{ Blocked, SecureReads []string }
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) > 0 {
				t.Fatal(err, string(output))
			}
			if source.Registration.Name != "usage" || source.Registration.Kind != "local-jsx" || len(source.Registration.Availability) != 1 || source.Registration.Availability[0] != "claude-ai" {
				t.Fatal("actual registry changed", string(output))
			}
			if goCalls.Load() != sourceCalls.Load() || int(goCalls.Load()) != len(source.Requests) {
				t.Fatal("gate disagreed", goCalls.Load(), sourceCalls.Load(), string(output))
			}
			for _, request := range source.Requests {
				if request.URL != "https://api.anthropic.com/api/oauth/usage" || request.Method != "get" || request.Timeout != 5000 {
					t.Fatal("original transport authority/budget", request)
				}
			}
			if source.Error != nil {
				var failure *clibootstrap.TerminalUsageHTTPError
				if !errors.As(goErr, &failure) || failure.Status != status || terminalUsageError(goErr) != "Failed to load usage data: "+string(source.Error.Body) {
					t.Fatal("actual error response consumer", goErr, string(output))
				}
			} else {
				if goErr != nil {
					t.Fatal(goErr)
				}
				left, _ := protocol.ParseAndStringifyJavaScriptJSON(got.Get("utilization"))
				right, _ := protocol.ParseAndStringifyJavaScriptJSON(source.Utilization)
				if !bytes.Equal(left, right) {
					t.Fatal("actual API consumer", string(left), string(right), string(output))
				}
			}
			if string(got.Get("extraUsageCommandEnabled")) != string(raw(source.ExtraEnabled)) {
				t.Fatal("cached identity extra usage gate", got, source.ExtraEnabled)
			}
			if name == "expired" && !strings.Contains(string(got.Get("utilization")), "null") {
				t.Fatal("expired must stay loading without refresh", got)
			}
		})
	}
}
