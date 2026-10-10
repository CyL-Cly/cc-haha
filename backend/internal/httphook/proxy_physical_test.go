package httphook

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceHTTPEnvironmentProxyTunnelsHTTPAndHTTPSAndSkipsTargetDNS(t *testing.T) {
	var posts, connects, forwards atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"proxy":"actual-body"}` || r.Method != "POST" {
			t.Error("actual tunneled request", string(body), r.Method)
		}
		_, _ = io.WriteString(w, `{"actual":"tunneled"}`)
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			forwards.Add(1)
			if r.URL.String() != "http://10.0.0.1:31337/original" || r.Header.Get("Proxy-Authorization") != "Basic ZmFrZTpwYXNz" {
				t.Error("original HTTP forward proxy request", r.URL, r.Header.Get("Proxy-Authorization"))
			}
			body, _ := io.ReadAll(r.Body)
			request, _ := http.NewRequest(http.MethodPost, plain.URL, bytes.NewReader(body))
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		connects.Add(1)
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic ZmFrZTpwYXNz" {
			t.Error("original environment proxy tunnel/auth", r.Method, r.Header.Get("Proxy-Authorization"))
			w.WriteHeader(400)
			return
		}
		target := ""
		switch r.Host {
		case "10.0.0.1:31337":
			target = strings.TrimPrefix(plain.URL, "http://")
		case strings.TrimPrefix(secure.URL, "https://"):
			target = strings.TrimPrefix(secure.URL, "https://")
		}
		if target == "" {
			t.Error("unexpected proxy target", r.Host)
			w.WriteHeader(400)
			return
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); upstream.Close(); close(done) }()
		_, _ = io.Copy(connection, upstream)
		connection.Close()
		<-done
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("fake", "pass")
	type row struct {
		Hook     protocol.JSONObject `json:"hook"`
		Settings protocol.JSONObject `json:"settings"`
		Body     string              `json:"body"`
	}
	rows := []row{}
	native := []Result{}
	for _, endpoint := range []string{"http://10.0.0.1:31337/original", secure.URL + "/original"} {
		hook := httpObject(t, `{"url":`+string(quote(endpoint))+`}`)
		body := `{"proxy":"actual-body"}`
		options := NativeOptions{Environment: map[string]string{"HTTPS_PROXY": proxyURL.String()}, VerifiedTLS: secure.Client().Transport.(*http.Transport).TLSClientConfig, Resolve: func(context.Context, string) ([]net.IPAddr, error) {
			t.Error("proxied target DNS/SSRF was called")
			return nil, io.EOF
		}}
		out, err := Execute(context.Background(), hook, protocol.NewJSONObject(), []byte(body), options)
		if err != nil || !out.OK {
			t.Fatal(out, err)
		}
		native = append(native, out)
		rows = append(rows, row{hook, protocol.NewJSONObject(), body})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-hook-transport.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_HOOK_TRANSPORT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secure.Certificate().Raw}))
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "ca": ca, "proxy": proxyURL.String()})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct{ Rows []Result }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(native) {
		t.Fatal(err, string(output))
	}
	for i := range native {
		actual, _ := json.Marshal(native[i])
		want, _ := json.Marshal(source.Rows[i])
		if !bytes.Equal(actual, want) {
			t.Errorf("actual Source proxy%d Go%s Source%s", i, actual, want)
		}
	}
	if connects.Load() != 2 || forwards.Load() != 2 || posts.Load() != 4 {
		t.Fatal("physical original proxy requests", connects.Load(), forwards.Load(), posts.Load())
	}
}

func TestHTTPProxyConnectCancellationAndNoProxyBypass(t *testing.T) {
	entered := make(chan struct{})
	ended := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(ended) }))
	defer proxy.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	hook := httpObject(t, `{"url":"https://10.0.0.1:31337/hook"}`)
	go func() {
		out, err := Execute(ctx, hook, protocol.NewJSONObject(), []byte(`{}`), NativeOptions{Environment: map[string]string{"HTTPS_PROXY": proxy.URL}, VerifiedTLS: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}})
		if err != nil {
			t.Error(err)
		}
		done <- out
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("physical CONNECT did not enter proxy")
	}
	cancel()
	select {
	case out := <-done:
		if !out.Aborted {
			t.Fatal(out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CONNECT cancellation did not join")
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy retained canceled socket")
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer peer.Close()
	hook.Set("url", quote(peer.URL))
	out, err := Execute(context.Background(), hook, protocol.NewJSONObject(), []byte(`{}`), NativeOptions{Environment: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1", "NO_PROXY": "127.0.0.1"}})
	if err != nil || !out.OK {
		t.Fatal("original bypass", out, err)
	}
}
