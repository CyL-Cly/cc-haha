package sandboxruntime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActualSourceParentVerifiedTLSTunnelAuthenticationHeadersAndBufferedBytes(t *testing.T) {
	var wg sync.WaitGroup
	headers := make(chan string, 2)
	prefix := []byte{0, 255, 128, 1, 2, 3}
	payload := bytes.Repeat([]byte{1, 0, 255, 128, 'z'}, 2048)
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		wg.Add(1)
		defer wg.Done()
		defer c.Close()
		headers <- fmt.Sprintf("%s|%s|%s|%s|%s", r.Method, r.Host, r.RequestURI, r.Header.Get("Proxy-Authorization"), r.Header.Get("User-Agent"))
		c.Write(append([]byte("HTTP/1.1 200 Established\r\n\r\n"), prefix...))
		io.Copy(c, b)
	}))
	defer peer.Close()
	pool := x509.NewCertPool()
	pool.AddCert(peer.Certificate())
	verified := &tls.Config{RootCAs: pool}
	proxy := parseParentProxy(map[string]string{"HTTPS_PROXY": "https://user:p%40ss@" + peer.Listener.Addr().String()})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	connection, e := openParentTunnel(ctx, proxy.https, "target.owned.test", 443, verified, (&net.Dialer{}).DialContext)
	if e != nil {
		t.Fatal(e)
	}
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	connection.Write(payload)
	native := make([]byte, len(prefix)+len(payload))
	if _, e = io.ReadFull(connection, native); e != nil {
		connection.Close()
		t.Fatal(e)
	}
	connection.Close()
	home := t.TempDir()
	cert := filepath.Join(home, "ca.pem")
	if e = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("testdata/parent-tunnel.fixture.ts")
	input, _ := json.Marshal(map[string]any{"proxy": proxy.https.String(), "hex": hex.EncodeToString(payload), "prefix": hex.EncodeToString(prefix)})
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "NODE_EXTRA_CA_CERTS=" + cert, "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct{ Hex string }
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	if hex.EncodeToString(native) != source.Hex || !bytes.Equal(native, append(prefix, payload...)) {
		t.Fatal("encrypted tunnel lost/reordered actual original bytes")
	}
	one, two := <-headers, <-headers
	if one != two || one != "CONNECT|target.owned.test:443|target.owned.test:443|Basic dXNlcjpwQHNz|" {
		t.Fatal("actual original CONNECT wire headers", one, two)
	}
	wg.Wait()
}

func TestNativeSandboxParentTunnelCloseCancelsHeldTLSAndJoinsPhysicalRequest(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		close(started)
		defer close(finished)
		c.Write([]byte("HTTP/1.1 200 Established\r\n\r\n"))
		_, _ = b.ReadByte()
	}))
	defer peer.Close()
	pool := x509.NewCertPool()
	pool.AddCert(peer.Certificate())
	n, e := NewNetwork(context.Background(), NetworkConfig{AllowedDomains: []string{"target.owned.test"}, DeniedDomains: []string{}}, NetworkOptions{Environment: map[string]string{"HTTPS_PROXY": peer.URL}, VerifiedTLS: &tls.Config{RootCAs: pool}})
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close(context.Background())
	port, _ := n.Ports()
	client, e := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%.0f", port))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(client, "CONNECT target.owned.test:443 HTTP/1.1\r\nHost: target.owned.test:443\r\n\r\n")
	response, e := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodConnect})
	if e != nil || response.StatusCode != 200 {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("physical parent TLS did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = n.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("Close did not release actual upstream TLS tunnel")
	}
	if e = n.Close(ctx); e != nil {
		t.Fatal("second Close did not share completion", e)
	}
}
