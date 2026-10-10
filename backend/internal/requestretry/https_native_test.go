package requestretry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// This is a deliberately public fixture trust anchor/key, copied only into
// disposable test state. No real provider, user certificate or OS store is read.
func originalPrivateTLSHost(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []byte) {
	t.Helper()
	path := "../../../scripts/quality-gate/go-parity/testdata/"
	certificate, err := tls.LoadX509KeyPair(path+"private-loopback-certificate.pem", path+"private-loopback-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(path + "private-loopback-certificate.pem")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return server, pem
}

func TestOriginalHTTPSRetryHoldsCapturedCAAfterFileReplacementAndClear(t *testing.T) {
	var posts atomic.Int32
	bodies := []string{}
	server, pem := originalPrivateTLSHost(t, func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(body))
		number := posts.Add(1)
		if request.RequestURI != "/v1/messages?beta=true" || request.Header.Get("X-Api-Key") != "offline-original-env-key" || request.TLS.NegotiatedProtocol != "http/1.1" {
			t.Error("HTTPS changed request/auth/protocol", request.RequestURI, request.TLS.NegotiatedProtocol)
		}
		if number < 3 {
			status := 500
			if number == 2 {
				status = 429
				writer.Header().Set("Retry-After", "2")
			}
			writer.WriteHeader(status)
			_, _ = io.WriteString(writer, `{"message":"physical TLS retry"}`)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, responseWire)
	})
	ca := filepath.Join(t.TempDir(), "original-ca.pem")
	if err := os.WriteFile(ca, pem, 0600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	state, _ := tlsconfig.NewState(func(path string) ([]byte, error) { reads++; return os.ReadFile(path) })
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.URL
	input.Context.Host.Env["NODE_EXTRA_CA_CERTS"] = ca
	deps.TLSState = state
	clients, prepared, notices, failures := 0, 0, 0, 0
	deps.OnClient = func() { clients++ }
	deps.OnPreparation = func([]protocol.JSONObject) { prepared++ }
	deps.ObserveFailure = func(err error) {
		failures++
		var sdk *provider.SDKStreamError
		if !errors.As(err, &sdk) || sdk.Status == nil {
			t.Error("observer replaced physical HTTP SDK error", err)
		}
	}
	deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) {
		notices++
		if notices == 1 {
			if err := os.WriteFile(ca, []byte("malformed replacement"), 0600); err != nil {
				return 0, err
			}
			state.Clear()
		}
		return Resume, nil
	}
	before, _ := json.Marshal(input.Context.History)
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal("old client's captured CA was re-read after cache clear", err)
	}
	defer stream.Close()
	parts := 0
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		parts++
	}
	after, _ := json.Marshal(input.Context.History)
	if reads != 1 || clients != 1 || prepared != 1 || notices != 2 || failures != 2 || posts.Load() != 3 || parts != 6 || string(before) != string(after) {
		t.Fatalf("TLS changed shared preparation/retry/cache ownership: reads%d client%d prepare%d notice%d failures%d posts%d parts%d", reads, clients, prepared, notices, failures, posts.Load(), parts)
	}
	if len(bodies) != 3 || !reflect.DeepEqual(bodies, []string{bodies[0], bodies[0], bodies[0]}) {
		t.Fatal("physical TLS retry changed prepared body bytes", bodies)
	}
	// A new client after Clear sees the new file; its fetch fails before a POST.
	input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "0"
	deps = standardDeps()
	deps.TLSState = state
	_, err = Open(context.Background(), input, deps)
	var native *provider.TLSConnectionFault
	if !errors.As(err, &native) || native.Code() != "FailedToOpenSocket" || reads != 2 || posts.Load() != 3 {
		t.Fatal("new client ignored cleared original CA memo", err, reads, posts.Load())
	}
}

func TestOriginalHTTPSCAFailuresRetainNativeSDKRecordWithoutPOST(t *testing.T) {
	for _, mode := range []string{"malformed", "no extra CA", "missing file"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			server, _ := originalPrivateTLSHost(t, func(http.ResponseWriter, *http.Request) { posts.Add(1) })
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.URL
			input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "0"
			ca := filepath.Join(t.TempDir(), "original-ca.pem")
			if mode != "no extra CA" {
				input.Context.Host.Env["NODE_EXTRA_CA_CERTS"] = ca
			}
			if mode == "malformed" {
				if err := os.WriteFile(ca, []byte("not a certificate"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reads, clients, requests, failureCalls := 0, 0, 0, 0
			state, _ := tlsconfig.NewState(func(path string) ([]byte, error) { reads++; return os.ReadFile(path) })
			deps.TLSState = state
			deps.OnClient = func() { clients++ }
			deps.ObserveRequest = func(string, http.Header, json.RawMessage) { requests++ }
			var originalError error
			deps.ObserveFailure = func(err error) { failureCalls++; originalError = err }
			_, err := Open(context.Background(), input, deps)
			var sdk *provider.SDKStreamError
			var native *provider.TLSConnectionFault
			if !errors.As(err, &sdk) || !errors.As(err, &native) || sdk.Class != "APIConnectionError" || sdk.Message != "Connection error." || sdk.Cause != native || sdk.Status != nil || sdk.Payload != nil || sdk.Headers != nil || errors.Unwrap(native) != nil {
				t.Fatal("original certificate failure changed SDK/native ownership", err)
			}
			code := "DEPTH_ZERO_SELF_SIGNED_CERT"
			wantedReads := 1
			if mode == "malformed" {
				code = "FailedToOpenSocket"
			} else if mode == "no extra CA" {
				wantedReads = 0
			}
			if native.Code() != code || reads != wantedReads || clients != 1 || requests != 1 || failureCalls != 1 || originalError != sdk || posts.Load() != 0 {
				t.Fatal("fetch/client/read/POST failure boundary changed", native.Code(), reads, clients, requests, failureCalls, posts.Load())
			}
			if _, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "changed-after-cache"}); err != nil || reads != wantedReads {
				t.Fatal("failure replaced process memo with a file re-read", err, reads)
			}
		})
	}
}

func TestOriginalTLSCapabilitiesFailBeforeSharedPreparationOrClient(t *testing.T) {
	for _, mode := range []string{"missing TLS state", "TLS on HTTP", "system CA", "mTLS", "insecure", "native Node option"} {
		t.Run(mode, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = "https://127.0.0.1:1"
			state, _ := tlsconfig.NewState(func(string) ([]byte, error) { t.Fatal("unported TLS read original file"); return nil, nil })
			deps.TLSState = state
			switch mode {
			case "missing TLS state":
				deps.TLSState = nil
			case "TLS on HTTP":
				input.Context.Host.Env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:1"
				input.Context.Host.Env["NODE_EXTRA_CA_CERTS"] = "original.pem"
			case "system CA":
				input.Context.Host.Env["NODE_OPTIONS"] = "--use-system-ca"
			case "mTLS":
				input.Context.Host.Env["CLAUDE_CODE_CLIENT_CERT"] = "original.pem"
			case "insecure":
				input.Context.Host.Env["NODE_TLS_REJECT_UNAUTHORIZED"] = "0"
			case "native Node option":
				input.Context.Host.Env["NODE_OPTIONS"] = "--tls-min-v1.0"
			}
			deps.OnPreparation = func([]protocol.JSONObject) { t.Fatal("unported TLS prepared history") }
			deps.OnKeyLookup = func() { t.Fatal("unported TLS looked up credentials") }
			deps.OnClient = func() { t.Fatal("unported TLS constructed client") }
			_, err := Open(context.Background(), input, deps)
			var cap *CapabilityError
			if !errors.As(err, &cap) {
				t.Fatal("unported TLS did not stop explicitly", err)
			}
		})
	}
}
