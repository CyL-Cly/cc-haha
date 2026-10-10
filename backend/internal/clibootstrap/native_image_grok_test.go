package clibootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
)

func TestNativeGrokImageOriginalEndpointsForced401RefreshAtomicFileAndGeneratedOutput(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private image OAuth trust"}, DNSNames: []string{"auth.x.ai", "api.x.ai"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var refreshes, images, tunnels atomic.Int32
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		switch r.Host {
		case "auth.x.ai":
			refreshes.Add(1)
			if r.URL.Path != "/oauth2/token" || string(data) != "grant_type=refresh_token&client_id=private-source-client&refresh_token=private-before-refresh" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "cc-haha-grok-oauth/1.0" {
				t.Error("actual Grok refresh envelope", r.Host, r.URL.Path, string(data), r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"private-after-access","refresh_token":"private-after-refresh","expires_in":3600}`)
		case "api.x.ai":
			number := images.Add(1)
			if r.URL.Path != "/v1/images/generations" {
				t.Error("actual Grok image endpoint", r.URL.Path)
			}
			if number == 1 {
				if r.Header.Get("Authorization") != "Bearer private-before-access" {
					t.Error("before image auth")
				}
				http.Error(w, "expired", 401)
				return
			}
			if r.Header.Get("Authorization") != "Bearer private-after-access" {
				t.Error("after image auth")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(nativeImagePNG))
		default:
			t.Error("unexpected Grok native target", r.Host)
			http.Error(w, "private target", 500)
		}
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "auth.x.ai:443" && r.Host != "api.x.ai:443" {
			t.Error("private Grok tunnel target", r.Method, r.Host)
			http.Error(w, "private target", 400)
			return
		}
		upstream, err := net.Dial("tcp", peer.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			http.Error(w, "private dial", 500)
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			_ = upstream.Close()
			return
		}
		tunnels.Add(1)
		defer downstream.Close()
		defer upstream.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); close(done) }()
		_, _ = io.Copy(downstream, upstream)
		_ = upstream.Close()
		_ = downstream.Close()
		<-done
	}))
	defer proxy.Close()
	m := openAIAuthMainFixture(t)
	path, caPath := filepath.Join(m.options.Native.Home, "private-grok.json"), filepath.Join(m.options.Native.Home, "private-grok-ca.pem")
	if err := os.WriteFile(path, rawMain(map[string]any{"accessToken": "private-before-access", "refreshToken": "private-before-refresh", "expiresAt": m.options.Native.NowMillis() + 3600000, "clientId": "private-source-client", "future": true}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	var output imagegeneration.Output
	err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for key, value := range map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "grok_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": "grok-official", "CC_HAHA_IMAGE_MODEL": "grok-imagine-image-quality", "GROK_OAUTH_FILE": path, "HTTPS_PROXY": proxy.URL, "NODE_EXTRA_CA_CERTS": caPath} {
			if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
				return err
			}
		}
		raw, err := m.nativeTools.Call(context.Background(), "ImageGen", webObject(`{"prompt":"actual private Grok image","count":1}`), scope, false)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &output)
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 || images.Load() != 2 || tunnels.Load() != 3 || len(output.Images) != 1 {
		t.Fatal("actual 401 Grok lifecycle", refreshes.Load(), images.Load(), tunnels.Load(), output)
	}
	tokens, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(tokens), "private-after-access") || strings.Contains(string(tokens), "future") {
		t.Fatal("Grok atomic normalized token file", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Grok refreshed token mode", err)
	}
	temporary, _ := filepath.Glob(path + ".tmp.*")
	if len(temporary) != 0 {
		t.Fatal("Grok temp credential leak", temporary)
	}
	data, err := os.ReadFile(output.Images[0].Path)
	if err != nil || string(data) != string(nativeImagePNG) {
		t.Fatal("Grok actual image artifact", err)
	}
}
