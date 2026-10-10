package clibootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSharedMainVertexSkipAuthWireThenNativeWriteRead(t *testing.T) {
	var models atomic.Int32
	var target string
	failures := make(chan error, 10)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := int(models.Add(1))
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if body.UnmarshalJSON(raw) != nil || body.Get("model") != nil || string(body.Get("stream")) != "true" || body.String("anthropic_version") != "vertex-2023-10-16" || r.URL.Path != "/v1/projects/private-project/locations/private-region/publishers/anthropic/models/claude-sonnet-4@20250514:streamRawPredict" || r.Header.Get("Authorization") != "" {
			failures <- fmt.Errorf("wrong original Vertex wire path/body/auth: path=%s version=%s stream=%s", r.URL.Path, body.String("anthropic_version"), body.Get("stream"))
			w.WriteHeader(400)
			return
		}
		if !strings.Contains(r.Header.Get("Anthropic-Beta"), "web-search-2025-03-05") {
			failures <- fmt.Errorf("Vertex model beta missing")
		}
		if round == 1 {
			emitNativeTaskTool(w, "vertex-write", "Write", map[string]any{"file_path": target, "content": "VERTEX_ACTUAL_SHARED_HARNESS"})
		} else if round == 2 {
			emitNativeTaskTool(w, "vertex-read", "Read", map[string]any{"file_path": target})
		} else {
			if !strings.Contains(string(raw), "VERTEX_ACTUAL_SHARED_HARNESS") {
				failures <- fmt.Errorf("actual Read missing from next model POST")
			}
			emitPlanFixture(w, round, "", "VERTEX_WRITE_READ_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "vertex-write-read.txt")
		for key, value := range map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_SKIP_VERTEX_AUTH": "1", "ANTHROPIC_VERTEX_BASE_URL": peer.URL + "/v1", "ANTHROPIC_VERTEX_PROJECT_ID": "private-project", "CLOUD_ML_REGION": "private-region"} {
			n.Environment[key] = value
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "claude-sonnet-4@20250514"
		o.Arguments.Tools = []string{"Write,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Write the marker and read it with the actual native file tools."})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if models.Load() != 3 {
		t.Fatal("actual Vertex model rounds", models.Load())
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "VERTEX_ACTUAL_SHARED_HARNESS" {
		t.Fatal("actual native artifact", string(raw), err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}

// All public hostnames terminate inside this private TLS peer. Only the native
// socket/TLS/proxy transport and ADC producer are real; no cloud account is used.
func TestActualSharedMainVertexADCNativeTLSProxyThenWriteReadAndClose(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private ADC trust"}, DNSNames: []string{"oauth2.googleapis.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var tokens, tunnels, models atomic.Int32
	var target string
	failures := make(chan error, 20)
	tokenPeer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.Host != "oauth2.googleapis.com" || r.URL.Path != "/token" || r.Method != "POST" || string(data) != "refresh_token=private-refresh&client_id=private-client&client_secret=private-secret&grant_type=refresh_token" || r.Header.Get("Authorization") != "" {
			failures <- fmt.Errorf("wrong physical ADC token request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"private-google-access","token_type":"Bearer","expires_in":3600}`)
	}))
	tokenPeer.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, NextProtos: []string{"http/1.1"}}
	tokenPeer.StartTLS()
	defer tokenPeer.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "oauth2.googleapis.com:443" {
			failures <- fmt.Errorf("ADC escaped private tunnel %s %s", r.Method, r.Host)
			http.Error(w, "private target", 400)
			return
		}
		upstream, err := net.Dial("tcp", tokenPeer.Listener.Addr().String())
		if err != nil {
			failures <- err
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			failures <- err
			_ = upstream.Close()
			return
		}
		tunnels.Add(1)
		defer tunnels.Add(-1)
		defer downstream.Close()
		defer upstream.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(downstream, upstream)
		_ = upstream.Close()
		_ = downstream.Close()
		<-done
	}))
	defer proxy.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := int(models.Add(1))
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if body.UnmarshalJSON(raw) != nil || body.Get("model") != nil || body.String("anthropic_version") != "vertex-2023-10-16" || r.URL.Path != "/v1/projects/private-project/locations/private-region/publishers/anthropic/models/claude-sonnet-4@20250514:streamRawPredict" || r.Header.Get("Authorization") != "Bearer private-google-access" || r.Header.Get("X-Goog-User-Project") != "quota-private" {
			failures <- fmt.Errorf("actual Vertex ADC headers/body/target mismatch")
			http.Error(w, "private wire", 400)
			return
		}
		if round == 1 {
			emitNativeTaskTool(w, "vertex-adc-write", "Write", map[string]any{"file_path": target, "content": "VERTEX_ADC_NATIVE_TLS"})
		} else if round == 2 {
			emitNativeTaskTool(w, "vertex-adc-read", "Read", map[string]any{"file_path": target})
		} else {
			if !strings.Contains(string(raw), "VERTEX_ADC_NATIVE_TLS") {
				failures <- fmt.Errorf("physical Read absent")
			}
			emitPlanFixture(w, round, "", "VERTEX_ADC_FINISHED")
		}
	}))
	defer peer.Close()
	var adcPath string
	adc := []byte(`{"type":"authorized_user","client_id":"private-client","client_secret":"private-secret","refresh_token":"private-refresh","quota_project_id":"quota-private","future":{"keep":true}}`)
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "vertex-adc-native.txt")
		adcPath = filepath.Join(n.Home, "adc-private.json")
		caPath := filepath.Join(n.Home, "google-ca-private.pem")
		if err := os.WriteFile(adcPath, adc, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "ANTHROPIC_VERTEX_BASE_URL": peer.URL + "/v1", "ANTHROPIC_VERTEX_PROJECT_ID": "private-project", "GCLOUD_PROJECT": "private-project", "CLOUD_ML_REGION": "private-region", "GOOGLE_APPLICATION_CREDENTIALS": adcPath, "HTTPS_PROXY": proxy.URL, "NO_PROXY": "127.0.0.1", "NODE_EXTRA_CA_CERTS": caPath} {
			n.Environment[key] = value
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "claude-sonnet-4@20250514"
		o.Arguments.Tools = []string{"Write,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Write and read the actual native marker using Vertex."})})
	if err = m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if tokens.Load() != 3 || models.Load() != 3 {
		t.Fatal("actual SDK fresh client token/model sequence", tokens.Load(), models.Load())
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "VERTEX_ADC_NATIVE_TLS" {
		t.Fatal("actual native artifact", string(raw), err)
	}
	saved, err := os.ReadFile(adcPath)
	if err != nil || string(saved) != string(adc) {
		t.Fatal("ADC file mutated", err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); tunnels.Load() != 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if tunnels.Load() != 0 {
		t.Fatal("native TLS tunnel escaped Shutdown", tunnels.Load())
	}
}
