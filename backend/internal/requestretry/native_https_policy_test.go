package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func nativeScopedOpen(t *testing.T, ctx context.Context, input Input, deps Dependencies) (stream *provider.RawMessageStream, err error) {
	t.Helper()
	owner := cooperative.New()
	t.Cleanup(func() { owner.Close() })
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		deps.NativeOpenIO = providernative.NewFactory(owner, scope, nil)
		stream, err = Open(ctx, input, deps)
		return err
	})
	return
}

func TestNativeHTTPSRetryHoldsCapturedCAAfterFileReplacementAndClear(t *testing.T) {
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
	stream, err := nativeScopedOpen(t, context.Background(), input, deps)
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
	_, err = nativeScopedOpen(t, context.Background(), input, deps)
	var native *provider.TLSConnectionFault
	if !errors.As(err, &native) || native.Code() != "FailedToOpenSocket" || reads != 2 || posts.Load() != 3 {
		t.Fatal("new client ignored cleared original CA memo", err, reads, posts.Load())
	}
}

func TestNativeHTTPSCAFailuresRetainNativeSDKRecordWithoutPOST(t *testing.T) {
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
			_, err := nativeScopedOpen(t, context.Background(), input, deps)
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
