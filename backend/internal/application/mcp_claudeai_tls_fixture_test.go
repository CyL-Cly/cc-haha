package application

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Keep the original production API/proxy URLs and certificate checks. Only
// the explicitly configured private CONNECT network primitive routes to the
// owned TLS peer, with a disposable extra CA trusted by both real callers.
func claudeAIAPIPrivateTLS(t *testing.T, handler http.Handler) (string, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "private CLI fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"api.anthropic.com", "mcp-proxy.anthropic.com"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})
	pair, e := tls.X509KeyPair(certPEM, privatePEM)
	if e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(t.TempDir(), "private-ca.pem")
	if e = os.WriteFile(ca, certPEM, 0600); e != nil {
		t.Fatal(e)
	}
	peer := httptest.NewUnstartedServer(handler)
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	peer.StartTLS()
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var joined sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || (r.Host != "api.anthropic.com:443" && r.Host != "mcp-proxy.anthropic.com:443") {
			t.Error("unowned CONNECT destination", r.Method, r.Host)
			w.WriteHeader(403)
			return
		}
		upstream, e := net.Dial("tcp", peer.Listener.Addr().String())
		if e != nil {
			t.Error(e)
			w.WriteHeader(502)
			return
		}
		client, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			upstream.Close()
			t.Error(e)
			return
		}
		mu.Lock()
		connections[client] = true
		connections[upstream] = true
		mu.Unlock()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if e = buffer.Flush(); e != nil {
			t.Error(e)
		}
		joined.Add(1)
		go func() {
			defer joined.Done()
			defer client.Close()
			defer upstream.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(upstream, buffer); _ = upstream.Close(); close(done) }()
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
			<-done
			mu.Lock()
			delete(connections, client)
			delete(connections, upstream)
			mu.Unlock()
		}()
	}))
	t.Cleanup(func() {
		mu.Lock()
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		proxy.Close()
		peer.CloseClientConnections()
		peer.Close()
		joined.Wait()
	})
	return proxy.URL, ca
}
