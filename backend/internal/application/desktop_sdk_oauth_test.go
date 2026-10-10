package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDedicatedSDKOAuthRecoveryUsesActualOwnedServiceAndSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	for _, row := range []struct {
		Name     string
		Official bool
		Token    string
	}{{"official", true, "fake-old"}, {"custom", false, "fake-old"}, {"missing-token", true, ""}, {"already-rotated", true, "fake-stale"}} {
		t.Run(row.Name, func(t *testing.T) {
			var calls atomic.Int32
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/api/oauth/profile" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"organization":{"organization_type":"claude_pro","rate_limit_tier":"pro"},"account":{"display_name":"Fixture"}}`))
					return
				}
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/oauth/token" || !bytes.Contains(body, []byte(`"refresh_token":"fake-refresh"`)) {
					t.Error("unexpected owned OAuth request", r.URL.Path, string(body))
				}
				time.Sleep(10 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"fake-rotated","refresh_token":"fake-new-refresh","expires_in":7200,"scope":"user:inference"}`))
			}))
			defer host.Close()
			tokens := obj([]byte(`{"accessToken":"fake-old","refreshToken":"fake-refresh","expiresAt":1900000000000,"scopes":["user:inference"],"subscriptionType":"pro"}`))
			sourceHome := t.TempDir()
			_ = os.MkdirAll(filepath.Join(sourceHome, ".claude"), 0700)
			_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-oauth.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = sourceHome
			cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_SDK_OAUTH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "USER_TYPE=ant", "USE_LOCAL_OAUTH=1", "CLAUDE_LOCAL_OAUTH_API_BASE=" + host.URL, "CLAUDE_LOCAL_OAUTH_APPS_BASE=" + host.URL, "CLAUDE_LOCAL_OAUTH_CONSOLE_BASE=" + host.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": sourceHome, "tokens": tokens, "official": row.Official, "token": row.Token}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Sent, Forwarded []json.RawMessage
				Token           string
			}
			if err = json.Unmarshal(out, &source); err != nil {
				t.Fatal(err, string(out))
			}
			sourceCalls := calls.Swap(0)
			home := t.TempDir()
			env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "USER_TYPE": "ant", "USE_LOCAL_OAUTH": "1", "CLAUDE_LOCAL_OAUTH_API_BASE": host.URL, "CLAUDE_LOCAL_OAUTH_APPS_BASE": host.URL, "CLAUDE_LOCAL_OAUTH_CONSOLE_BASE": host.URL}
			oauth, err := desktopoauth.New(desktopoauth.Options{Home: home, ConfigDir: env["CLAUDE_CONFIG_DIR"], Platform: "linux", Environment: func() map[string]string { return env }, CCROAuthTokenPath: filepath.Join(home, "remote", ".oauth_token")})
			if err != nil {
				t.Fatal(err)
			}
			defer oauth.Close()
			if err = oauth.SaveTokens(ctx, tokens); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			sent := []json.RawMessage{}
			forwarded := []json.RawMessage{}
			nativeEnv := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": row.Token, "CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}
			if row.Official {
				nativeEnv["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop"
			}
			d := &DesktopSession{ctx: ctx, engine: &Session{native: &clibootstrap.NativeBoundary{Environment: nativeEnv}}, emit: func(json.RawMessage) {}, projection: NewProjection(), onSDK: func(data json.RawMessage) { forwarded = append(forwarded, data) }, options: Options{SDKRecoverOfficialOAuth: func(ctx context.Context, rejected string) (*string, error) {
				doc, e := oauth.RecoverFromUnauthorized(ctx, rejected)
				if e != nil || doc == nil {
					return nil, e
				}
				token := doc.Record().String("accessToken")
				return &token, nil
			}}}
			unbind := d.BindSDKResponseTransport(func(data json.RawMessage) error { mu.Lock(); sent = append(sent, data); mu.Unlock(); return nil })
			defer unbind()
			finishPayload := d.BeginSDKPayload()
			for i := 0; i < 20; i++ {
				if err = d.AcceptSDKOutput(ctx, json.RawMessage(`{"type":"system","subtype":"api_retry","error_status":" 401 "}`)); err != nil {
					t.Fatal(err)
				}
			}
			finishPayload()
			state := d.sdkRuntime()
			state.mu.Lock()
			done := state.recoveryDone
			state.mu.Unlock()
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			mu.Lock()
			actualSent := append([]json.RawMessage{}, sent...)
			mu.Unlock()
			compare := func(name string, actual, expected any) {
				var a, b any
				_ = json.Unmarshal(raw(actual), &a)
				_ = json.Unmarshal(raw(expected), &b)
				aa, _ := json.Marshal(a)
				bb, _ := json.Marshal(b)
				if !bytes.Equal(aa, bb) {
					t.Errorf("%s Source%s Go%s", name, bb, aa)
				}
			}
			compare("transport", actualSent, source.Sent)
			compare("forwarded", forwarded, source.Forwarded)
			if calls.Load() != sourceCalls {
				t.Errorf("refresh Source%d Go%d", sourceCalls, calls.Load())
			}
			_ = d.closeSDKTransport(ctx)
		})
	}
}

func TestDedicatedSDKOAuthRecoveryCloseCancelsAndDoesNotEmitStaleToken(t *testing.T) {
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sent atomic.Int32
	d := &DesktopSession{ctx: ctx, engine: &Session{native: &clibootstrap.NativeBoundary{Environment: map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CLAUDE_CODE_OAUTH_TOKEN": "fake-old"}}}, options: Options{SDKRecoverOfficialOAuth: func(ctx context.Context, _ string) (*string, error) {
		close(entered)
		<-ctx.Done()
		fresh := "fake-stale-completed"
		return &fresh, nil
	}}}
	d.BindSDKResponseTransport(func(json.RawMessage) error { sent.Add(1); return nil })
	if e := d.recoverSDKOAuth(); e != nil {
		t.Fatal(e)
	}
	<-entered
	if e := d.closeSDKTransport(context.Background()); e != nil {
		t.Fatal(e)
	}
	if sent.Load() != 0 {
		t.Fatal("stale credential sent after close")
	}
}
