package providernative

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/klauspost/compress/zstd"
)

func privateOpenAITrust(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private OpenAI loopback"}, DNSNames: []string{"chatgpt.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, roots
}
func privateOpenAIProxy(t *testing.T, target string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "chatgpt.com:443" {
			t.Error("wrong physical proxy target", r.Method, r.Host)
			w.WriteHeader(400)
			return
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		var joined sync.WaitGroup
		joined.Add(1)
		go func() { defer joined.Done(); _, _ = io.Copy(upstream, buffer); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		_ = upstream.Close()
		joined.Wait()
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

type openAIPhysicalTrace struct {
	input   tracecapture.FetchInput
	done    chan struct{}
	body    []byte
	failure error
}

func (trace *openAIPhysicalTrace) HeadersResponse(_ int, _ http.Header, body io.ReadCloser) {
	go func() { defer close(trace.done); defer body.Close(); trace.body, trace.failure = io.ReadAll(body) }()
}
func (trace *openAIPhysicalTrace) FetchFailure(failure error) {
	trace.failure = failure
	close(trace.done)
}
func openAIPhysicalJSON(raw string) protocol.JSONObject {
	var value protocol.JSONObject
	_ = json.Unmarshal([]byte(raw), &value)
	return value
}

func TestOpenAICodexNativeOwnerStreamKeepsTraceAfterSDKCleanup(t *testing.T) {
	certificate, roots := privateOpenAITrust(t)
	releaseTail := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseTail) })
	requestBody := make(chan []byte, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("content-encoding") != "zstd" || r.Header.Get("authorization") != "Bearer private-access" {
			t.Error("remapped physical request lost", r.URL, r.Header)
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
		requestBody <- plain
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Codex-Turn-State", "private-first-routing")
		w.Header().Set("X-Request-Id", "private-upstream-request")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"response\":{\"id\":\"private-response\",\"model\":\"gpt-6-sol\"}}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"response\":{\"id\":\"private-response\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"private-call\",\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\\\"private.txt\\\"}\"}],\"usage\":{\"input_tokens\":31,\"output_tokens\":8}}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-releaseTail:
			_, _ = io.WriteString(w, ": private-trace-tail\n\n")
		case <-r.Context().Done():
			t.Error("normal SDK cleanup cancelled terminal upstream trace")
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	server.StartTLS()
	defer server.Close()
	defer server.CloseClientConnections()
	proxy := privateOpenAIProxy(t, server.Listener.Addr().String())
	owner := cooperative.New()
	defer owner.Close()
	trace := &openAIPhysicalTrace{done: make(chan struct{})}
	turn := providerproxy.NewOpenAICodexTurnState(context.Background())
	var eventTypes []string
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		})
		if err != nil {
			return err
		}
		client := provider.NewAnthropic(provider.AnthropicConfig{APIKey: providerproxy.OpenAIOAuthDummyKey, Timeout: 5 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{ProxyURL: proxy.URL, VerifiedTLS: &tls.Config{RootCAs: roots}}, OwnerNativeIO: driver, OwnerBodyIO: bodyIO, DefaultHeaders: http.Header{"X-Claude-Code-Session-Id": []string{"private-root"}}, TraceBegin: func(input tracecapture.FetchInput) tracecapture.FetchCapture { trace.input = input; return trace }, OpenAICodex: &provider.OpenAICodexConfig{Env: map[string]string{}, FreshTokens: func(context.Context) (protocol.JSONObject, error) {
			return openAIPhysicalJSON(`{"accessToken":"private-access","refreshToken":"private-refresh","expiresAt":1000000000000000}`), nil
		}, TurnState: turn, NowMillis: func() int64 { return 1700000000000 }, Encode: func(plain []byte) ([]byte, error) {
			return scope.AwaitBytes(cooperative.SDKOutput, func(context.Context) ([]byte, error) { return providerproxy.EncodeOpenAIZstd(plain) })
		}}})
		raw, err := client.OpenMessagesStream(context.Background(), &provider.MessagesRequest{Path: "/v1/messages", Body: []byte(`{"model":"gpt-6-sol","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"private input"}]}`)})
		if err != nil {
			return err
		}
		defer raw.Close()
		if raw.Headers.Get(providerproxy.OpenAICodexStreamMarkerHeader) != "1" || raw.RequestID != nil {
			return errors.New("remapped Source stream metadata changed")
		}
		stream, err := provider.NewOwnedMessageStream(scope, owner, raw)
		if err != nil {
			return err
		}
		for {
			pending, err := stream.Iterator.Next(scope)
			if err != nil {
				return err
			}
			result, err := pending.Await(scope)
			if err != nil {
				return err
			}
			if result.Done {
				break
			}
			kind := result.Value.Object.String("type")
			eventTypes = append(eventTypes, kind)
			if kind == "message_stop" {
				break
			}
		}
		closing, err := stream.Iterator.ReturnUndefined(scope)
		if err != nil {
			return err
		}
		if _, err = closing.Await(scope); err != nil {
			return err
		}
		if err = stream.AbortSDK(scope); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		release.Do(func() { close(releaseTail) })
		t.Fatal(err)
	}
	release.Do(func() { close(releaseTail) })
	select {
	case <-trace.done:
	case <-time.After(5 * time.Second):
		t.Fatal("actual original trace clone did not drain")
	}
	if trace.failure != nil || !strings.Contains(string(trace.body), "private-trace-tail") {
		t.Fatal("completed upstream trace changed", trace.failure, string(trace.body))
	}
	if turn.Get() != "private-first-routing" {
		t.Fatal("agentic turn state lost", turn.Get())
	}
	if !strings.Contains(strings.Join(eventTypes, ","), "content_block_start,content_block_delta,content_block_stop,message_delta,message_stop") {
		t.Fatal("actual native tool stream lost", eventTypes)
	}
	select {
	case plain := <-requestBody:
		if string(plain) != string(trace.input.PlainBody) {
			t.Fatal("trace has compressed or different request")
		}
	case <-time.After(time.Second):
		t.Fatal("physical request missing")
	}
	if trace.input.EncodingMetadata.String("requestEncoding") != "zstd" {
		t.Fatal("encoding audit missing")
	}
}
