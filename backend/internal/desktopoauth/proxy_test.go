package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestActualSourceRefreshHTTPProxyAndProfileFailure(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	h := host(t)
	s, home, env, clock := fixture(t, h)
	var mu sync.Mutex
	requests := []string{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.Host)
		mu.Unlock()
		if r.Method != http.MethodConnect {
			forward := r.Clone(r.Context())
			forward.RequestURI = ""
			response, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(forward)
			if err != nil {
				w.WriteHeader(502)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				w.Header()[key] = values
			}
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		target, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = target.Close()
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		go func() { _, _ = io.Copy(target, buffer); _ = target.Close() }()
		_, _ = io.Copy(client, target)
		_ = client.Close()
	}))
	defer proxy.Close()
	env["CC_HAHA_SYSTEM_PROXY_URL"] = proxy.URL
	saveFixture(t, s, tokens("fake-old", "fake-refresh", *clock-1).record())
	fresh, err := s.EnsureFreshTokens(context.Background())
	if err != nil || fresh == nil {
		t.Fatalf("Go proxied refresh %v", err)
	}
	profile, err := s.options.FetchProfile(context.Background(), "fake-profile-token")
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.profileFail = true
	h.mu.Unlock()
	failedProfile, err := s.options.FetchProfile(context.Background(), "fake-profile-token")
	if err != nil || failedProfile.Get("rawProfile") != nil {
		t.Fatal("failed profile must omit rawProfile")
	}
	mu.Lock()
	goRequests := append([]string(nil), requests...)
	requests = nil
	mu.Unlock()
	h.mu.Lock()
	h.profileFail = false
	h.mu.Unlock()
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-oauth.fixture.ts")
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + env["CLAUDE_CONFIG_DIR"], "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_OAUTH_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	for _, key := range []string{"USER_TYPE", "USE_LOCAL_OAUTH", "CLAUDE_LOCAL_OAUTH_API_BASE", "CLAUDE_LOCAL_OAUTH_APPS_BASE", "CLAUDE_LOCAL_OAUTH_CONSOLE_BASE", "CC_HAHA_SYSTEM_PROXY_URL"} {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	actions := []map[string]any{{"kind": "save", "tokens": json.RawMessage(tokens("fake-old", "fake-refresh", *clock-1).record())}, {"kind": "ensure"}}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"clock": *clock, "actions": actions, "models": []string{}, "subscriptions": []any{}, "scopes": []string{}, "auth": []any{}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source proxied refresh %v %s", err, limit(stderr.String()))
	}
	var source struct {
		Values []struct {
			Tokens json.RawMessage `json:"tokens"`
		} `json:"values"`
	}
	if json.Unmarshal(output, &source) != nil || len(source.Values) != 2 {
		t.Fatalf("Source result %s", limit(string(output)))
	}
	var a, b any
	_ = json.Unmarshal(fresh.Data, &a)
	_ = json.Unmarshal(source.Values[1].Tokens, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("refresh Go=%s Source=%s", fresh.Data, source.Values[1].Tokens)
	}
	mu.Lock()
	sourceRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(goRequests) != 4 || len(sourceRequests) != 2 || !reflect.DeepEqual(goRequests[:2], sourceRequests) {
		t.Fatalf("Proxy wire Go=%v Source=%v", goRequests, sourceRequests)
	}
	if profile.String("subscriptionType") != "max" {
		t.Fatal("proxy profile mapping")
	}
}

func TestExchangeNoProxyOriginalPatterns(t *testing.T) {
	for _, test := range []struct {
		address, noProxy string
		want             bool
	}{
		{"http://localhost:42", "", true}, {"https://127.0.0.1", "", true}, {"http://[::1]", "", true},
		{"https://example.test", ".example.test", true}, {"https://a.example.test", ".example.test", true}, {"https://notexample.test", ".example.test", false},
		{"https://example.test", "example.test:443", true}, {"https://example.test", "example.test:80", false}, {"https://[fd00::1234]", "[fd00::1234]:443", true},
		{"https://example.test", "*", true}, {"https://example.test", "other.test", false},
	} {
		if got := exchangeBypassesProxy(test.address, map[string]string{"NO_PROXY": test.noProxy}); got != test.want {
			t.Errorf("%s noProxy=%q got=%v", test.address, test.noProxy, got)
		}
	}
}
