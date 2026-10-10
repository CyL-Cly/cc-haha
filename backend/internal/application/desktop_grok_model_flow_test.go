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
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func grokDesktopCertificate(t *testing.T) (tls.Certificate, []byte) {
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

func TestActualDesktopGrokFactoryCatalogWSWriteAndClose(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	target := filepath.Join(o.WorkDir, "grok-desktop-artifact.txt")
	certificate, ca := grokDesktopCertificate(t)
	caFile := filepath.Join(home, "grok-private-ca.pem")
	if err := os.WriteFile(caFile, ca, 0600); err != nil {
		t.Fatal(err)
	}
	pendingGrok, grokClosed := make(chan struct{}), make(chan struct{})
	var models, posts atomic.Int32
	var second atomic.Value
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "cli-chat-proxy.grok.com" || r.Header.Get("Authorization") != "Bearer fake-desktop-grok" {
			t.Error("desktop lost token authority", r.Host, r.Header)
			http.Error(w, "authority", 400)
			return
		}
		if r.URL.Path == "/v1/models" {
			models.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{"id":"grok-4.7","display_name":"Private live Grok catalog","context_window":600001,"supportsReasoningEffort":true,"reasoningEffort":"high","reasoningEfforts":["high","low"]}]}`)
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Error("unknown desktop Grok endpoint", r.URL)
			http.Error(w, "endpoint", 400)
			return
		}
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		body := obj(wire)
		if body.String("model") != "grok-4.7" || r.Header.Get("x-grok-model-override") != "grok-4.7" || r.Header.Get("x-grok-conv-id") == "" {
			t.Error("actual Grok model/cache envelope", string(wire), r.Header)
		}
		n := posts.Add(1)
		var output any
		if n == 1 {
			output = []any{map[string]any{"type": "function_call", "call_id": "grok-desktop-write", "name": "Write", "arguments": string(raw(map[string]string{"file_path": target, "content": "GROK_DESKTOP_FACTORY_FILE\n"}))}}
		} else if n == 2 {
			second.Store(wire)
			output = []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": "GROK_DESKTOP_FACTORY_OK"}}}}
		} else if n == 3 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.created\ndata: {\"response\":{\"id\":\"grok-held-response\"}}\n\n")
			w.(http.Flusher).Flush()
			close(pendingGrok)
			<-r.Context().Done()
			close(grokClosed)
			return
		} else {
			t.Error("second Harness/unexpected model turn", n)
			http.Error(w, "extra", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", raw(map[string]any{"response": map[string]any{"id": "grok-desktop-response", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 17, "output_tokens": 6}}}))
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "cli-chat-proxy.grok.com:443" {
			t.Error("escaped private desktop tunnel", r.Method, r.Host)
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
	o.Environment["DISABLE_TELEMETRY"] = "1"
	delete(o.Environment, "ANTHROPIC_BASE_URL")
	delete(o.Environment, "ANTHROPIC_API_KEY")
	o.Environment["HTTPS_PROXY"] = proxy.URL
	o.Environment["NODE_EXTRA_CA_CERTS"] = caFile
	tokenFile := filepath.Join(config, "cc-haha", "grok-oauth.json")
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, raw(map[string]any{"accessToken": "fake-desktop-grok", "refreshToken": "fake-desktop-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "email": "fake@invalid"}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path string, body any, status int) protocol.JSONObject {
		t.Helper()
		r, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(raw(body)))
		r.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s status%d %s %v", method, path, response.StatusCode, data, err)
		}
		return obj(data)
	}
	request("POST", "/api/providers/grok-official/activate", map[string]any{}, 200)
	if string(request("GET", "/api/providers/auth-status", nil, 200).Get("hasAuth")) != "true" {
		t.Fatal("actual desktop account not bound")
	}
	end := time.Now().Add(3 * time.Second)
	discovered := false
	for time.Now().Before(end) {
		catalog := request("GET", "/api/models", nil, 200)
		if bytes.Contains(catalog.Get("models"), []byte("Private live Grok catalog")) {
			discovered = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !discovered || models.Load() != 1 {
		t.Fatal("actual catalog refresh did not bind", models.Load())
	}
	created := request("POST", "/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201)
	id := created.String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	_ = websocket.Message.Send(ws, string(raw(map[string]any{"type": "set_runtime_config", "providerId": "grok-official", "modelId": "grok-4.7", "effortLevel": "high"})))
	applied := false
	for i := 0; i < 100; i++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		value := obj([]byte(line))
		if value.String("type") == "error" {
			t.Fatal(line)
		}
		if value.String("type") == "runtime_config_applied" {
			applied = true
			break
		}
	}
	if !applied {
		t.Fatal("real Grok WS model/effort not applied")
	}
	_ = websocket.Message.Send(ws, string(raw(map[string]string{"type": "user_message", "content": "Write the private artifact then finish."})))
	finished := false
	for i := 0; i < 250; i++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		value := obj([]byte(line))
		if value.String("type") == "error" {
			t.Fatal(line)
		}
		finished = finished || value.String("type") == "message_complete"
		if finished && value.String("type") == "session_state" && value.String("turnState") == "idle" {
			break
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "GROK_DESKTOP_FACTORY_FILE\n" || posts.Load() != 2 || !finished {
		t.Fatal("actual Grok factory tool turn", err, string(data), posts.Load(), finished)
	}
	if value := second.Load(); value == nil || !bytes.Contains(value.([]byte), []byte("grok-desktop-write")) {
		t.Fatal("same Harness tool result lost")
	}
	_ = websocket.Message.Send(ws, string(raw(map[string]string{"type": "user_message", "content": "Wait while I stop this Grok stream."})))
	select {
	case <-pendingGrok:
	case <-time.After(3 * time.Second):
		t.Fatal("actual Grok pending stream did not start")
	}
	if err = websocket.Message.Send(ws, `{"type":"stop_generation"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-grokClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not close physical Grok request")
	}
	stopped := false
	for i := 0; i < 100; i++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		value := obj([]byte(line))
		if value.String("type") == "session_state" && value.String("turnState") == "idle" {
			stopped = true
			break
		}
	}
	if !stopped {
		t.Fatal("actual WS cancellation did not settle")
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
