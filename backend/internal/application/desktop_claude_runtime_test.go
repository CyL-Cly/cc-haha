package application

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func claudeDesktopCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private Claude runtime"}, DNSNames: []string{"api.anthropic.com", "platform.claude.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	data, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{data}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data})
}

func TestActualDesktopClaudeOfficialTokenAndSubscriptionReachMainPrint(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]

	expectedBeta := claudeClientSourceHeader(t, home, config)
	target := filepath.Join(o.WorkDir, "claude-official-artifact.txt")
	certificate, ca := claudeDesktopCertificate(t)
	caPath := filepath.Join(home, "private-claude-ca.pem")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	var calls, refreshes, profiles atomic.Int32
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "platform.claude.com" && r.URL.Path == "/v1/oauth/token" {
			body, _ := io.ReadAll(r.Body)
			value := obj(body)
			if value.String("grant_type") != "refresh_token" || value.String("refresh_token") != "fake-refresh" || value.String("client_id") != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" || value.String("scope") != "user:inference" {
				t.Error("actual OAuth refresh body", string(body))
			}
			token := "fake-managed-official"
			if refreshes.Add(1) == 2 {
				token = "fake-managed-next"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, string(raw(map[string]any{"access_token": token, "refresh_token": nil, "expires_in": 3600, "scope": "user:inference"})))
			return
		}
		if r.Host == "api.anthropic.com" && r.URL.Path == "/api/oauth/profile" {
			profiles.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"organization":{"organization_type":"claude_pro","rate_limit_tier":"default_claude_pro"},"account":{}}`)
			return
		}
		expectedToken := "Bearer fake-managed-official"
		if calls.Load() >= 2 {
			expectedToken = "Bearer fake-managed-next"
		}
		if r.Host != "api.anthropic.com" || r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != expectedToken || r.Header.Get("X-Api-Key") != "" {
			t.Error("actual Claude request lost official authority", r.Host, r.URL.Path, r.Header.Get("X-Api-Key"))
			http.Error(w, "authority", 400)
			return
		}
		if r.Header.Get("Anthropic-Beta") != expectedBeta {
			t.Errorf("actual Claude beta Source %q Go %q", expectedBeta, r.Header.Get("Anthropic-Beta"))
		}
		data, _ := io.ReadAll(r.Body)
		if obj(data).String("model") != "claude-sonnet-5-5" {
			t.Error("subscription model did not reach real model request", obj(data).String("model"))
		}
		n := calls.Add(1)
		if n == 1 {
			response(w, "claude-write", "Write", map[string]string{"file_path": target, "content": "CLAUDE_OFFICIAL_PHYSICAL\n"}, "")
		} else if n == 2 {
			if !bytes.Contains(data, []byte(target)) {
				t.Error("actual Write tool result missing")
			}
			response(w, "claude-final", "", nil, "CLAUDE_OFFICIAL_DONE")
		} else if n == 3 || n == 4 {
			if !bytes.Contains(data, []byte(target)) {
				t.Error("same shared MainPrint lost canonical history")
			}
			response(w, "claude-next", "", nil, "CLAUDE_COLD_AND_REFRESH_DONE")
		} else {
			t.Error("unexpected model request", n)
			http.Error(w, "extra", 500)
		}
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || (r.Host != "api.anthropic.com:443" && r.Host != "platform.claude.com:443") {
			t.Error("escaped private official tunnel", r.Host)
			http.Error(w, "denied", 400)
			return
		}
		upstream, err := net.Dial("tcp", peer.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			t.Error(err)
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		var joined sync.WaitGroup
		joined.Add(1)
		go func() { defer joined.Done(); _, _ = io.Copy(upstream, buffer); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		_ = upstream.Close()
		joined.Wait()
	}))
	defer proxy.Close()
	o.Environment["HTTPS_PROXY"] = proxy.URL
	o.Environment["NODE_EXTRA_CA_CERTS"] = caPath
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Native = nil
	o.Arguments.Model = ""
	delete(o.Environment, "ANTHROPIC_BASE_URL")
	delete(o.Environment, "ANTHROPIC_API_KEY")
	o.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-inherited-must-be-discarded"
	tokenPath := filepath.Join(config, "cc-haha", "oauth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(`{"accessToken":"fake-managed-old","refreshToken":"fake-refresh","expiresAt":0,"scopes":["user:inference"],"subscriptionType":"pro","future":{"retain":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The same concrete OAuth producer is constructed by NewDesktopServer. The
	// test below first exposes the missing binding without issuing any model HTTP.
	constructed := make(chan map[string]string, 8)
	factory := func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		if env["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] == "claude-desktop" {
			constructed <- env
		}
		native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if native != nil {
			// Replace only remote/MDM policy reads with the disposable absent-policy
			// primitive. Official policy synchronization remains outside this test.
			native.ResolvePolicy = func() (*protocol.JSONObject, error) { return nil, nil }
			native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
		}
		return native, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	oauthTransport := http.DefaultTransport.(*http.Transport).Clone()
	oauthTransport.TLSClientConfig = &tls.Config{RootCAs: pool}
	defer oauthTransport.CloseIdleConnections()
	createServer := func() (*server.Server, error) {
		return NewDesktopServer(ServerOptions{ClaudeOAuthHTTPClient: &http.Client{Transport: oauthTransport}, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: factory, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }})
	}
	s, err := createServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	response, err := http.Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]string{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("create %d %s", response.StatusCode, data)
	}
	id := obj(data).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err = websocket.Message.Send(ws, `{"type":"prewarm_session"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case env := <-constructed:
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != "fake-managed-official" || env["CLAUDE_CODE_ENTRYPOINT"] != "claude-desktop" {
			t.Fatalf("actual desktop factory did not bind managed OAuth: tokenMatch=%t entrypoint=%q", env["CLAUDE_CODE_OAUTH_TOKEN"] == "fake-managed-official", env["CLAUDE_CODE_ENTRYPOINT"])
		}
	case <-time.After(20 * time.Second):
		t.Fatal("actual desktop factory did not start")
	}
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	if err = websocket.Message.Send(ws, `{"type":"user_message","content":"Write the requested private file and report completion."}`); err != nil {
		t.Fatal(err)
	}
	finished := false
	for i := 0; i < 200; i++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		frame := obj([]byte(line))
		if frame.String("type") == "error" {
			t.Fatal("actual MainPrint failed", line)
		}
		if frame.String("type") == "session_state" && frame.String("turnState") == "idle" && calls.Load() == 2 {
			finished = true
			break
		}
	}
	data, err = os.ReadFile(target)
	if !finished || err != nil || string(data) != "CLAUDE_OFFICIAL_PHYSICAL\n" || calls.Load() != 2 {
		t.Fatalf("actual Claude shared tools failed finished=%t calls=%d file=%q err=%v", finished, calls.Load(), data, err)
	}

	// Rotate the real old JSON before the next turn. Source sends its existing
	// update_environment_variables rather than constructing another CLI/Harness.
	if err = os.WriteFile(tokenPath, []byte(`{"accessToken":"fake-managed-official","refreshToken":"fake-refresh","expiresAt":0,"scopes":["user:inference"],"subscriptionType":"pro"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runNext := func(socket *websocket.Conn, want int32) {
		t.Helper()
		_ = socket.SetDeadline(time.Now().Add(20 * time.Second))
		if err := websocket.Message.Send(socket, `{"type":"user_message","content":"Use the prior real Write result and finish the next turn."}`); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 200; i++ {
			var line string
			if err := websocket.Message.Receive(socket, &line); err != nil {
				t.Fatal(err)
			}
			frame := obj([]byte(line))
			if frame.String("type") == "error" {
				t.Fatal(line)
			}
			if frame.String("type") == "session_state" && frame.String("turnState") == "idle" && calls.Load() == want {
				return
			}
		}
		t.Fatal("actual next turn never finished")
	}
	// A real WS null-provider restart resolves the legacy saved alias before
	// starting the replacement owner, rather than sending Opus on a Pro account.
	if err = websocket.Message.Send(ws, `{"type":"set_runtime_config","providerId":null,"modelId":"opus[1m]"}`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		frame := obj([]byte(line))
		if frame.String("type") == "error" {
			t.Fatal(line)
		}
		if frame.String("type") == "runtime_config_applied" {
			break
		}
		if i == 199 {
			t.Fatal("null runtime restart not applied")
		}
	}
	runNext(ws, 3)
	if refreshes.Load() != 2 || profiles.Load() != 2 {
		t.Fatalf("real refresh/profile count %d/%d", refreshes.Load(), profiles.Load())
	}
	stored, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if obj(stored).String("accessToken") != "fake-managed-next" || obj(stored).String("refreshToken") != "fake-refresh" || obj(stored).Get("future") != nil {
		t.Fatal("Source refreshed-token shape changed", string(stored))
	}
	mode, err := os.Stat(tokenPath)
	if err != nil || mode.Mode().Perm() != 0600 {
		t.Fatal("private OAuth token permissions", err)
	}
	_ = ws.Close()
	host.Close()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A fresh factory/service loads the same files and same SID, without another
	// refresh request. The reopened real MainPrint must send the preceding tools.
	cold, err := createServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close(context.Background())
	coldHost := httptest.NewServer(cold)
	defer coldHost.Close()
	coldWS, err := websocket.Dial("ws"+strings.TrimPrefix(coldHost.URL, "http")+"/ws/"+id, "", coldHost.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer coldWS.Close()
	runNext(coldWS, 4)
	after, err := os.ReadFile(tokenPath)
	if err != nil || !bytes.Equal(stored, after) || refreshes.Load() != 2 {
		t.Fatal("cold OAuth restore mutated/refreshed fresh credentials", err)
	}
}

func claudeClientSourceHeader(t *testing.T, home, config string) string {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response(w, "source-claude-final", "", nil, "SOURCE_DONE")
	}))
	defer peer.Close()
	fixture, _ := filepath.Abs("../../testdata/native-claude-client-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "CLAUDE_CODE_OAUTH_TOKEN=fake-managed-official", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"origin": peer.URL, "models": []string{"claude-sonnet-5-5"}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source client %v %s", err, stderr.String())
	}
	var observed struct {
		Observations []struct {
			Captured struct {
				Headers [][]string
				Body    map[string]any
			}
			Betas []string
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &observed); err != nil || len(observed.Observations) != 1 || len(observed.Capabilities.Blocked) != 0 {
		t.Fatalf("Source client result %v %s", err, data)
	}
	headers := http.Header{}
	for _, row := range observed.Observations[0].Captured.Headers {
		headers.Add(row[0], row[1])
	}
	if headers.Get("Authorization") != "Bearer fake-managed-official" || headers.Get("X-Api-Key") != "" {
		t.Fatal("actual Source did not select OAuth")
	}
	return headers.Get("Anthropic-Beta")
}
