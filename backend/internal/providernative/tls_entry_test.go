package providernative

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestNativeTLSPreparedEntryHasActualVerifiedResponse(t *testing.T) {
	certificate, err := tls.LoadX509KeyPair("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem", "../../../scripts/quality-gate/go-parity/testdata/private-loopback-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("fixture roots missing")
	}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(request.Body)
		if request.RequestURI != "/v1/messages?beta=true" || request.TLS.NegotiatedProtocol != "http/1.1" || string(body) != `{"model":"fixture-model","stream":true}` {
			t.Error("original prepared TLS request changed", request.RequestURI, string(body))
		}
		response.Header().Set("Request-Id", "native-tls-entry")
		io.WriteString(response, "private-body")
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	server.StartTLS()
	defer server.CloseClientConnections()
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: server.URL, APIKey: "fake-native-tls-key", Timeout: 3 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}, OwnerNativeIO: driver})
		exchange, cancel, err := client.OpenPreparedResponse(context.Background(), &provider.MessagesRequest{Path: "/v1/messages?beta=true", Body: []byte(`{"model":"fixture-model","stream":true}`)})
		if err != nil {
			return err
		}
		defer cancel()
		defer exchange.Response.Body.Close()
		body, err := scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { return io.ReadAll(exchange.Response.Body) })
		if err != nil {
			return err
		}
		if string(body) != "private-body" || exchange.Response.Header.Get("request-id") != "native-tls-entry" {
			t.Error("verified TLS body/owner headers lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal("same Owner prepared HTTPS response is not migrated", err)
	}
	if requests.Load() != 1 {
		t.Fatal("TLS sends changed", requests.Load())
	}
}
