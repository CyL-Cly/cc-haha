package desktopoauth

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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func catalogTLSFixture(t *testing.T, handler http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(17941), Subject: pkix.Name{CommonName: "Owned Catalog TLS Fixture"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, DNSNames: []string{"localhost", "chatgpt.com"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewUnstartedServer(handler)
	host.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	host.StartTLS()
	return host, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func catalogConnectFixture(t *testing.T, target string, count *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			t.Error("catalog proxy used a non-CONNECT method")
			w.WriteHeader(400)
			return
		}
		if r.Host != target && r.Host != "chatgpt.com:443" {
			t.Error("unowned proxy target")
			w.WriteHeader(403)
			return
		}
		count.Add(1)
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			t.Error(err)
			return
		}
		defer client.Close()
		defer upstream.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = buffer.Flush()
		var workers sync.WaitGroup
		workers.Add(2)
		go func() { defer workers.Done(); _, _ = io.Copy(upstream, buffer); _ = upstream.Close() }()
		go func() { defer workers.Done(); _, _ = io.Copy(client, upstream); _ = client.Close() }()
		workers.Wait()
	}))
}

func TestOpenAICatalogDefaultTransportFrozenProxyCAAndActualBunFetch(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, profile := range []string{"proxy-upper", "proxy-lower-precedence", "no-proxy-exact", "no-proxy-star"} {
		t.Run(profile, func(t *testing.T) {
			var requests, connects atomic.Int64
			host, cert := catalogTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer fake-owned-access" {
					t.Error("owned bearer header changed")
				}
				_, _ = io.WriteString(w, `{"models":[{"slug":"physical-catalog","visibility":"list"}]}`)
			}))
			defer host.Close()
			u, _ := url.Parse(host.URL)
			proxy := catalogConnectFixture(t, u.Host, &connects)
			defer proxy.Close()
			home := t.TempDir()
			ca := filepath.Join(home, "ca.pem")
			if err := os.WriteFile(ca, cert, 0600); err != nil {
				t.Fatal(err)
			}
			environment := map[string]string{"HTTPS_PROXY": proxy.URL, "NODE_EXTRA_CA_CERTS": ca}
			if profile == "proxy-lower-precedence" {
				environment["https_proxy"] = proxy.URL
				environment["HTTPS_PROXY"] = "http://127.0.0.1:1"
			}
			if profile == "no-proxy-exact" {
				environment["NO_PROXY"] = "127.0.0.1"
			}
			if profile == "no-proxy-star" {
				environment["NO_PROXY"] = "*"
				environment["HTTPS_PROXY"] = "http://127.0.0.1:1"
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-catalog-fetch.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_CATALOG_FETCH_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			for key, value := range environment {
				command.Env = append(command.Env, key+"="+value)
			}
			command.Stdin = bytes.NewReader(encode(map[string]string{"url": host.URL + "/models"}))
			var stderr bytes.Buffer
			command.Stderr = &stderr
			data, err := command.Output()
			if err != nil {
				t.Fatalf("actual Bun fetch %v %s", err, stderr.String())
			}
			var source struct {
				Models []providerproxy.OpenAIModel
				Error  string
			}
			_ = json.Unmarshal(data, &source)
			if source.Error != "" {
				t.Fatalf("Source global fetch failed %s", source.Error)
			}
			sourceConnects := connects.Load()
			sourceRequests := requests.Load()
			parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return environment }})
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			client, err := parent.openAIModelHTTPClient()
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(ctx, "GET", host.URL+"/models", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fake-owned-access")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "physical-catalog") || requests.Load() != sourceRequests+1 || connects.Load() != sourceConnects*2 {
				t.Fatalf("original global fetch route mismatch SourceProxy=%d GoProxy=%d SourceRequests=%d GoRequests=%d", sourceConnects, connects.Load()-sourceConnects, sourceRequests, requests.Load()-sourceRequests)
			}
		})
	}
}

func TestOpenAICatalogDefaultProductionClientThroughOwnedProxyTLS(t *testing.T) {
	var requests, connects atomic.Int64
	host, cert := catalogTLSFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/backend-api/codex/models" || r.Header.Get("Authorization") != "Bearer fake-owned-access" {
			t.Error("production catalog request changed")
		}
		_, _ = io.WriteString(w, `{"models":[{"slug":"production-physical-catalog","visibility":"list"}]}`)
	}))
	defer host.Close()
	u, _ := url.Parse(host.URL)
	proxy := catalogConnectFixture(t, u.Host, &connects)
	defer proxy.Close()
	home := t.TempDir()
	ca := filepath.Join(home, "owned-ca.pem")
	if err := os.WriteFile(ca, cert, 0600); err != nil {
		t.Fatal(err)
	}
	parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string {
		return map[string]string{"HTTPS_PROXY": proxy.URL, "NODE_EXTRA_CA_CERTS": ca}
	}, Settings: func(context.Context) (protocol.JSONObject, error) {
		panic("catalog must not read editable network settings")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	service := parent.OpenAI()
	if err = service.SaveTokens(context.Background(), object([]byte(`{"accessToken":"fake-owned-access","expiresAt":null,"accountId":"owned-account"}`))); err != nil {
		t.Fatal(err)
	}
	models, err := service.ModelCatalog(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Value != "production-physical-catalog" || requests.Load() != 1 || connects.Load() != 1 {
		t.Fatal("default catalog client did not use frozen proxy, actual verified CA and official endpoint")
	}
}
