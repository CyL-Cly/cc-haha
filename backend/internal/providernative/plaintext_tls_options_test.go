package providernative

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestNativeHTTPSharedTLSOptionsAreUnconsumedWithoutChangingOwnedAuthRequest(t *testing.T) {
	for _, mode := range []string{"verified", "resolver", "both"} {
		t.Run(mode, func(t *testing.T) {
			origin, peer := nativePeer(t, func(conn net.Conn, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer private-cached-token" || len(r.Header.Values("X-Api-Key")) != 0 {
					t.Error("cached request auth changed", r.Header)
				}
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4\r\n\r\nsame")
			})
			owner := cooperative.New()
			defer owner.Close()
			verified := &tls.Config{}
			resolverCalls := 0
			exchange := &provider.RawHTTP1Exchange{}
			if mode != "resolver" {
				exchange.VerifiedTLS = verified
			}
			if mode != "verified" {
				exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { resolverCalls++; return nil, errors.New("unconsumed plaintext trust") }
			}
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				driver, err := New(owner, scope, nil)
				if err != nil {
					return err
				}
				config := nativeConfig(origin)
				config.APIKey = ""
				config.AuthToken = "private-cached-token"
				config.OwnerNativeIO = driver
				config.HTTPExchange = exchange
				response, cancel, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
				if err != nil {
					return err
				}
				defer cancel()
				return response.Response.Body.Close()
			}); err != nil {
				t.Fatal(err)
			}
			joinedPeer(t, peer)
			if resolverCalls != 0 || (mode != "resolver" && exchange.VerifiedTLS != verified) || (mode != "verified" && exchange.ResolveVerifiedTLS == nil) {
				t.Fatal("shared options were invoked or mutated", resolverCalls)
			}
		})
	}
}
