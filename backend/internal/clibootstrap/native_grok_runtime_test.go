package clibootstrap

import (
	"bytes"
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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func nativeGrokRuntimeCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private original OAuth transport"}, DNSNames: []string{"cli-chat-proxy.grok.com", "auth.x.ai"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}

func TestActualGrokCLIRefresh401ToolsAndCanonicalTranscript(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	certificate, ca := nativeGrokRuntimeCertificate(t)
	caPath, tokenPath, target := filepath.Join(f.home, "private-ca.pem"), filepath.Join(f.home, "private-grok.json"), filepath.Join(f.cwd, "actual-oauth-artifact.txt")
	f.write(t, caPath, string(ca))
	f.write(t, tokenPath, fmt.Sprintf(`{"accessToken":"private-old","refreshToken":"private-refresh","expiresAt":%d,"email":"private@invalid","accountId":"private-account","future":true}`, time.Now().UnixMilli()-1))
	var mu sync.Mutex
	refreshes, posts := 0, 0
	var requestHeaders []http.Header
	var requestBodies []protocol.JSONObject
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Host == "auth.x.ai" && r.URL.Path == "/oauth2/token" {
			refreshes++
			expectedRefresh := "private-refresh"
			access := "private-new"
			rotation := "private-rotated"
			if refreshes == 2 {
				expectedRefresh = "private-rotated"
				access = "private-forced"
				rotation = "private-final"
			}
			if string(wire) != "grant_type=refresh_token&client_id=b1a00492-073a-47ea-816f-4c329264a828&refresh_token="+expectedRefresh {
				t.Error("actual Grok refresh envelope", string(wire))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":3600}`, access, rotation)
			return
		}
		if r.Host != "cli-chat-proxy.grok.com" || r.URL.Path != "/v1/responses" {
			t.Error("wrong physical Grok request", r.Host, r.URL)
			http.Error(w, "wrong endpoint", 500)
			return
		}
		if r.Header.Get("Authorization") == "Bearer private-new" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, "private token rejected")
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-forced" || r.Header.Get("x-xai-token-auth") != "xai-grok-cli" || r.Header.Get("x-grok-client-version") != "1.0.46" || r.Header.Get("content-encoding") != "" {
			t.Error("wrong Grok identity", r.Header)
			http.Error(w, "identity mismatch", 500)
			return
		}
		plain := wire
		var body protocol.JSONObject
		if err = body.UnmarshalJSON(plain); err != nil {
			t.Error(err)
			return
		}
		posts++
		requestHeaders = append(requestHeaders, r.Header.Clone())
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "text/event-stream")

		var output any
		switch posts {
		case 1:
			output = []any{map[string]any{"type": "function_call", "call_id": "private-write", "name": "Write", "arguments": string(rawMain(map[string]string{"file_path": target, "content": "PRIVATE_OAUTH_PHYSICAL_ARTIFACT"}))}}
		case 2, 4:
			output = []any{map[string]any{"type": "function_call", "call_id": fmt.Sprintf("private-read-%d", posts), "name": "Read", "arguments": string(rawMain(map[string]string{"file_path": target}))}}
		case 3, 5:
			output = []any{map[string]any{"type": "message", "id": "private-complete", "content": []any{map[string]any{"type": "output_text", "text": "PRIVATE_OAUTH_TOOL_LOOP_OK"}}}}
		default:
			t.Error("unexpected model POST", posts)
			http.Error(w, "unexpected", 500)
			return
		}
		response := map[string]any{"id": fmt.Sprintf("private-response-%d", posts), "model": "grok-4.7", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 31, "output_tokens": 9}}
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", rawMain(map[string]any{"response": response}))
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	defer peer.CloseClientConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "cli-chat-proxy.grok.com:443" && r.Host != "auth.x.ai:443" {
			t.Error("escaped CONNECT", r.Method, r.Host)
			http.Error(w, "denied", 400)
			return
		}
		upstream, err := net.Dial("tcp", peer.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		client, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buf.Flush()
		var joined sync.WaitGroup
		joined.Add(1)
		go func() { defer joined.Done(); _, _ = io.Copy(upstream, buf); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		_ = upstream.Close()
		joined.Wait()
	}))
	defer proxy.Close()
	delete(f.n.Environment, "ANTHROPIC_BASE_URL")
	for key, value := range map[string]string{"GROK_OAUTH_FILE": tokenPath, "CC_HAHA_GROK_OAUTH_PROVIDER": "1", "ANTHROPIC_MODEL": "grok-4.7", "NODE_EXTRA_CA_CERTS": caPath, "HTTPS_PROXY": proxy.URL, "PATH": "/usr/bin:/bin", "CLAUDE_CODE_DISABLE_ATTACHMENTS": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		f.n.Environment[key] = value
	}
	disabled := false
	var output, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "grok-4.7", SystemPrompt: "Use the original tools in the private workspace.", Tools: []string{"Read,Write"}, MaxTurns: 6, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics, OnOutput: func(b []byte) error { output.Write(b); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.ShutdownSession(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	userTurns := []string{"Write and read the private artifact.", "Read it again in a new user turn."}
	for _, text := range userTurns {
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
		if err = m.RunTurn(ctx, user); err != nil {
			t.Fatalf("actual CLI OAuth turn %v %s", err, output.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 2 || posts != 5 {
		t.Fatalf("actual physical requests refresh%d/model%d output%s", refreshes, posts, output.String())
	}
	for i, h := range requestHeaders {
		if h.Get("x-grok-model-override") != "grok-4.7" || h.Get("x-grok-conv-id") == "" {
			t.Error("Grok model/cache identity missing", h)
		}
		if requestBodies[i].String("model") != "grok-4.7" {
			t.Error("model changed")
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "PRIVATE_OAUTH_PHYSICAL_ARTIFACT" {
		t.Fatal("tool did not write actual artifact", err, string(data))
	}
	tokens, err := os.ReadFile(tokenPath)
	if err != nil || !bytes.Contains(tokens, []byte(`"private-forced"`)) || bytes.Contains(tokens, []byte(`"future"`)) {
		t.Fatal("original refresh persistence", err, string(tokens))
	}
	info, _ := os.Stat(tokenPath)
	if info.Mode().Perm() != 0600 {
		t.Error("OAuth file mode", info.Mode())
	}
	if !strings.Contains(output.String(), "PRIVATE_OAUTH_TOOL_LOOP_OK") {
		t.Fatal("normal CLI result missing", output.String())
	}
	var transcript string
	_ = filepath.WalkDir(filepath.Join(f.config, "projects"), func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(path, m.Controller.SessionID()+".jsonl") {
			transcript = path
		}
		return nil
	})
	if transcript == "" {
		t.Fatal("canonical CLI session transcript missing")
	}
	log, err := os.ReadFile(transcript)
	if err != nil || !bytes.Contains(log, []byte("private-write")) || !bytes.Contains(log, []byte("PRIVATE_OAUTH_TOOL_LOOP_OK")) {
		t.Fatal("canonical transcript missed actual tools/result", err)
	}
}
