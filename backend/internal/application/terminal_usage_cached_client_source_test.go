package application

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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSourceCachedSubscriberClientExternalProfilePhysicalHeader(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned actual Source required")
	}
	for _, profile := range []string{"cached-subscriber", "external-key", "cached-clear", "retry-500", "retry-401"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, ".credentials.json"), raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-owned-cached-client-token", "refreshToken": nil, "scopes": []string{"user:inference", "user:profile"}, "expiresAt": 9999999999999, "subscriptionType": "max"}}), 0600); err != nil {
				t.Fatal(err)
			}
			retryStatus := 0
			if strings.HasPrefix(profile, "retry-") {
				retryStatus, _ = strconv.Atoi(strings.TrimPrefix(profile, "retry-"))
				if err := os.Remove(filepath.Join(config, ".credentials.json")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"oauthAccount":{"billingType":"stripe_subscription"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/v1/messages" || obj(body).String("model") != "claude-sonnet-4-5" {
					t.Error(r.Method, r.URL, string(body))
				}
				if profile == "cached-subscriber" || profile == "cached-clear" {
					if r.Header.Get("Authorization") != "Bearer fake-owned-cached-client-token" || r.Header.Get("x-api-key") != "" {
						t.Error("Actual cached auth header", r.Header)
					}
				} else if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "fake-owned-external-key" {
					t.Error("Actual external auth header", r.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				if count == 1 && retryStatus != 0 {
					w.WriteHeader(retryStatus)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"private owned creation retry"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"owned-cached-client","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"OWNED_CACHED_CLIENT_OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer peer.Close()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cached-subscriber-client.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "ANTHROPIC_BASE_URL=" + peer.URL, "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CACHED_CLIENT_FIXTURE=1"}
			if profile == "external-key" || retryStatus != 0 {
				cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY=fake-owned-external-key", "NODE_ENV=test")
			}
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_FAST_MODE=1")
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "peer": peer.URL, "clearBetween": profile == "cached-clear", "retryStatus": retryStatus}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var result struct {
				Subscriber                                  bool
				Requests                                    []json.RawMessage
				InitialSession, FinalSession, ParentSession string
				Caps                                        struct {
					Blocked     []string
					SecureReads []string
				}
			}
			if err = json.Unmarshal(output, &result); err != nil {
				t.Fatal(err, string(output))
			}
			want := 1
			if profile == "cached-clear" || retryStatus != 0 {
				want = 2
			}
			if result.Subscriber != (profile == "cached-subscriber" || profile == "cached-clear") || int(calls.Load()) != want || len(result.Requests) != want || len(result.Caps.Blocked) != 0 {
				t.Fatal(string(output), calls.Load())
			}
			if profile == "cached-clear" {
				before := obj(obj(result.Requests[0]).Get("headers")).String("x-claude-code-session-id")
				after := obj(obj(result.Requests[1]).Get("headers")).String("x-claude-code-session-id")
				if before != result.InitialSession || after != result.FinalSession || before == after || result.ParentSession != before {
					t.Fatal("Actual Source native clear did not re-sample client header", string(output))
				}
			}
			if retryStatus != 0 {
				before := obj(obj(result.Requests[0]).Get("headers")).String("x-claude-code-session-id")
				after := obj(obj(result.Requests[1]).Get("headers")).String("x-claude-code-session-id")
				wantAfter := result.InitialSession
				if retryStatus == 401 {
					wantAfter = result.FinalSession
				}
				metadata := obj(obj(obj(result.Requests[1]).Get("body")).Get("metadata"))
				encoded := metadata.String("user_id")
				if before != result.InitialSession || after != wantAfter || result.InitialSession == result.FinalSession || result.ParentSession != before || obj([]byte(encoded)).String("session_id") != result.FinalSession {
					t.Fatal("Actual Source client reuse/refresh or live metadata changed", profile, string(output))
				}
			}
		})
	}
}
