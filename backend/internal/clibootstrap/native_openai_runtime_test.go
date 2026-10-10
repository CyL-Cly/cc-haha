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
	"encoding/json"
	"encoding/pem"
	"fmt"
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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/klauspost/compress/zstd"
)

func nativeOpenAIRuntimeCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private original OAuth transport"}, DNSNames: []string{"chatgpt.com", "auth.openai.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}

func TestActualOpenAICLIRefreshToolsTurnStateAndCanonicalTranscript(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	certificate, ca := nativeOpenAIRuntimeCertificate(t)
	caPath, tokenPath, target := filepath.Join(f.home, "private-ca.pem"), filepath.Join(f.home, "private-openai.json"), filepath.Join(f.cwd, "actual-oauth-artifact.txt")
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
		if r.Host == "auth.openai.com" && r.URL.Path == "/oauth/token" {
			refreshes++
			if string(wire) != "grant_type=refresh_token&refresh_token=private-refresh&client_id=app_EMoamEEZ73f0CkXaXp7hrann&scope=openid+profile+email" {
				t.Error("actual refresh request differs", string(wire))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"private-new","expires_in":3600}`)
			return
		}
		if r.Host != "chatgpt.com" || r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer private-new" || r.Header.Get("content-encoding") != "zstd" {
			t.Error("wrong physical OAuth model request", r.Host, r.URL, r.Header)
			http.Error(w, "private protocol mismatch", 500)
			return
		}
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer decoder.Close()
		plain, err := decoder.DecodeAll(wire, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var body protocol.JSONObject
		if err = body.UnmarshalJSON(plain); err != nil {
			t.Error(err)
			return
		}
		posts++
		requestHeaders = append(requestHeaders, r.Header.Clone())
		requestBodies = append(requestBodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Codex-Turn-State", fmt.Sprintf("private-routing-%d", posts))
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
		response := map[string]any{"id": fmt.Sprintf("private-response-%d", posts), "model": "gpt-6-sol", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 31, "output_tokens": 9}}
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", rawMain(map[string]any{"response": response}))
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	defer peer.CloseClientConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "chatgpt.com:443" && r.Host != "auth.openai.com:443" {
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
	for key, value := range map[string]string{"OPENAI_CODEX_OAUTH_FILE": tokenPath, "CC_HAHA_OPENAI_OAUTH_PROVIDER": "1", "ANTHROPIC_MODEL": "gpt-6-sol", "NODE_EXTRA_CA_CERTS": caPath, "HTTPS_PROXY": proxy.URL, "NO_PROXY": "*", "PATH": "/usr/bin:/bin", "CLAUDE_CODE_DISABLE_ATTACHMENTS": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		f.n.Environment[key] = value
	}
	disabled := false
	var output, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "gpt-6-sol", SystemPrompt: "Use the original tools in the private workspace.", Tools: []string{"Read,Write"}, MaxTurns: 6, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics, OnOutput: func(b []byte) error { output.Write(b); return nil }})
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
	if refreshes != 1 || posts != 5 {
		t.Fatalf("actual physical requests refresh%d/model%d output%s", refreshes, posts, output.String())
	}
	for i, h := range requestHeaders {
		want := "private-routing-1"
		if i == 0 || i == 3 {
			want = ""
		}
		if i == 4 {
			want = "private-routing-4"
		}
		if h.Get("x-codex-turn-state") != want {
			t.Errorf("request%d routing %q want%q", i+1, h.Get("x-codex-turn-state"), want)
		}
		if h.Get("session-id") == "" || h.Get("session-id") != h.Get("thread-id") {
			t.Error("original identity lost", h)
		}
		if requestBodies[i].String("model") != "gpt-6-sol" {
			t.Error("model routing changed")
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "PRIVATE_OAUTH_PHYSICAL_ARTIFACT" {
		t.Fatal("tool did not write actual artifact", err, string(data))
	}
	tokens, err := os.ReadFile(tokenPath)
	if err != nil || !bytes.Contains(tokens, []byte(`"private-new"`)) || bytes.Contains(tokens, []byte(`"future"`)) {
		t.Fatal("original refresh persistence", err, string(tokens))
	}
	info, _ := os.Stat(tokenPath)
	if info.Mode().Perm() != 0600 {
		t.Error("OAuth file mode", info.Mode())
	}
	if !strings.Contains(output.String(), "PRIVATE_OAUTH_TOOL_LOOP_OK") {
		t.Fatal("normal CLI result missing", output.String())
	}
	wantSource := actualOpenAIInitAPIKeySource(t)
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	initCount := 0
	for {
		var message protocol.JSONObject
		if err := decoder.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal("actual SDK output", err)
		}
		if message.String("subtype") == "init" {
			initCount++
			if message.String("apiKeySource") != wantSource {
				t.Fatal("actual SDK init reported the wrong credential source", message.String("apiKeySource"), "want", wantSource)
			}
		}
	}
	if initCount != len(userTurns) {
		t.Fatal("actual SDK init count", initCount)
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

func actualOpenAIInitAPIKeySource(t *testing.T) string {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun is required for this OAuth SDK contract")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-model-selection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_OPENAI_MODEL_SELECTION_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = strings.NewReader(fmt.Sprintf(`{"home":%q,"cases":[{"env":{},"settings":{},"parse":[]}]}`, home))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual original init auth %v %s", err, stderr.String())
	}
	var result struct {
		Rows []struct {
			APIKeySource string `json:"apiKeySource"`
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Rows) != 1 || len(result.Capabilities.Blocked) > 0 {
		t.Fatalf("original init consumer %v %s", err, raw)
	}
	return result.Rows[0].APIKeySource
}
