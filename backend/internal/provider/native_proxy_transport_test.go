//go:build darwin

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func privateCONNECTProxy(t *testing.T, target string, connects *atomic.Int32) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "127.0.0.1:443" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Errorf("unexpected target or model credentials leaked to proxy: %s %s authPresent=%v", r.Method, r.Host, r.Header.Get("Authorization") != "")
			http.Error(w, "invalid", 400)
			return
		}
		connects.Add(1)
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		client, stream, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		if _, err = io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, stream); _ = upstream.(*net.TCPConn).CloseWrite(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func TestNativeHTTPProxyPreservesTLSAndOriginalRequestThroughDefaultPort(t *testing.T) {
	certificate, roots := privateTLSCertificate(t, "127.0.0.1")
	var sends, connects atomic.Int32
	server := privateTLSServer(t, certificate, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"original":true}` || r.Header.Get("Authorization") != "Bearer fake-owned-loopback-only" || r.URL.Path != "/v1/messages" {
			t.Errorf("original request changed: %s %s", r.URL.Path, body)
		}
		_, _ = io.WriteString(w, "original TLS response")
	})
	proxy := privateCONNECTProxy(t, server.Listener.Addr().String(), &connects)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://127.0.0.1/v1/messages", strings.NewReader(`{"original":true}`))
	request.Header.Set("Authorization", "Bearer fake-owned-loopback-only")
	exchange, err := NativeDirectHTTPExchangeFromEnvironment(request.URL.String(), map[string]string{"HTTPS_PROXY": proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	exchange.VerifiedTLS = &tls.Config{RootCAs: roots}
	response, err := exchange.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Response.Body)
	_ = response.Response.Body.Close()
	if err != nil || string(body) != "original TLS response" || sends.Load() != 1 || connects.Load() != 1 {
		t.Fatalf("proxy send/body: %s %v sends%d connects%d", body, err, sends.Load(), connects.Load())
	}
	// The actual production resource uses the same tunnel, publishes its native
	// descriptor before application bytes, and retains the upstream body prefix.
	var wire bytes.Buffer
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "https://127.0.0.1/v1/messages", strings.NewReader(`{"original":true}`))
	request.Header.Set("Authorization", "Bearer fake-owned-loopback-only")
	if err = request.Write(&wire); err != nil {
		t.Fatal(err)
	}
	trust, err := verifiedHTTP1TLS(exchange.VerifiedTLS, request.URL.Hostname())
	if err != nil {
		t.Fatal(err)
	}
	resource, err := newNativeTLSHTTP1ProxyReservation(ctx, nativeTargetAuthority(request.URL), wire.Bytes(), 64*1024, trust, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close()
	for {
		_, ready, err := resource.Checkpoint()
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if err = resource.WaitReadable(ctx); err != nil {
			t.Fatal(err)
		}
	}
	upstream, err := resource.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(upstream.Body)
	_ = upstream.Body.Close()
	if err != nil || string(body) != "original TLS response" || sends.Load() != 2 || connects.Load() != 2 {
		t.Fatalf("native proxy send/body: %s %v sends%d connects%d", body, err, sends.Load(), connects.Load())
	}
	if bun := os.Getenv("CC_HAHA_PINNED_BUN"); bun != "" {
		fixture, err := filepath.Abs("testdata/native-proxy-fetch.fixture.ts")
		if err != nil {
			t.Fatal(err)
		}
		certFile := filepath.Join(t.TempDir(), "proxy-ca.pem")
		if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		tmp := filepath.Join(home, "tmp")
		if err = os.MkdirAll(tmp, 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		command.Dir = home
		command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + tmp, "PATH=/usr/bin:/bin", "HTTPS_PROXY=" + proxy.URL, "NODE_EXTRA_CA_CERTS=" + certFile, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
		input, _ := json.Marshal(map[string]string{"url": request.URL.String()})
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("actual Source proxy fetch: %v %s", err, output)
		}
		var observed struct {
			Status int
			Body   string
		}
		if err = json.Unmarshal(output, &observed); err != nil || observed.Status != 200 || observed.Body != "original TLS response" || sends.Load() != 3 || connects.Load() != 3 {
			t.Fatalf("Source physical proxy result: %s %v sends%d connects%d", output, err, sends.Load(), connects.Load())
		}
	}
}

func TestHTTPProxyCancellationJoinsBeforeOriginSend(t *testing.T) {
	_, trust := tlsProbeConfiguration(t)
	accepted := make(chan struct{})
	closed := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, reader, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		close(accepted)
		_, _ = reader.ReadByte()
		close(closed)
	}))
	defer proxy.Close()
	ctx, cancel := context.WithCancel(context.Background())
	resource, err := newNativeTLSHTTP1ProxyReservation(ctx, "127.0.0.1:443", []byte("must never send"), 1024, trust, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy never accepted")
	}
	cancel()
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled CONNECT leaked physical connection")
	}
	if _, _, err = resource.Checkpoint(); !errors.Is(err, net.ErrClosed) {
		t.Fatal("closed proxy resource remained readable", err)
	}
}
