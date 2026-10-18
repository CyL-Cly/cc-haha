//go:build darwin

package providernative

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestActualSourcePreconnectAndClientSharePhysicalNativeHTTPPool(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun not configured")
	}
	for _, kind := range []string{"keepalive", "head-close", "head-failure", "tls", "keepalive-disabled"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var ports []string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				ports = append(ports, r.RemoteAddr)
				mu.Unlock()
				if r.Method == http.MethodHead {
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
						t.Error("HEAD inherited model auth")
					}
					if kind == "head-close" {
						w.Header().Set("Connection", "close")
					}
					if kind == "head-failure" {
						c, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = c.Close()
						return
					}
					w.WriteHeader(204)
					return
				}
				if r.Method != http.MethodPost {
					t.Error("unexpected method", r.Method)
				}
				_, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"owned-pool","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"OWNED_POOL_RESPONSE"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			})
			peer := httptest.NewUnstartedServer(handler)
			if kind == "tls" {
				peer.StartTLS()
			} else {
				peer.Start()
			}
			defer peer.Close()
			home := t.TempDir()
			if e := os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
				t.Fatal(e)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-pool.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_POOL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
			exchange := &provider.RawHTTP1Exchange{}
			if kind == "tls" {
				ca := filepath.Join(home, "pool-ca.pem")
				if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); e != nil {
					t.Fatal(e)
				}
				command.Env = append(command.Env, "NODE_EXTRA_CA_CERTS="+ca)
				roots := x509.NewCertPool()
				roots.AddCert(peer.Certificate())
				exchange.VerifiedTLS = &tls.Config{RootCAs: roots}
			}
			payload, _ := json.Marshal(map[string]any{"home": home, "peer": peer.URL, "keepaliveDisabled": kind == "keepalive-disabled"})
			command.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			data, e := command.Output()
			if e != nil {
				t.Fatal("whole Source preconnect/client", e, stderr.String())
			}
			var value struct {
				Responses int
				Blocked   []any
			}
			if e = json.Unmarshal(data, &value); e != nil || value.Responses != 2 || len(value.Blocked) != 0 {
				t.Fatal("Source result", e, string(data))
			}
			mu.Lock()
			expected := append([]string{}, ports...)
			ports = nil
			mu.Unlock()
			if len(expected) != 3 {
				t.Fatal("Source actual wire count", expected)
			}
			owner := cooperative.New()
			defer owner.Close()
			var pool *Pool
			e = owner.Run(ctx, func(scope *cooperative.Scope) error { var e error; pool, e = NewPool(owner, scope); return e })
			if e != nil {
				t.Fatal(e)
			}
			defer pool.Close()
			var transport http.RoundTripper
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				transport, e = pool.HeadTransport(owner, scope, exchange)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			head, _ := http.NewRequestWithContext(ctx, http.MethodHead, peer.URL, nil)
			response, headFailure := transport.RoundTrip(head)
			if kind == "head-failure" {
				if headFailure == nil {
					t.Fatal("physical HEAD failure suppressed")
				}
			} else if headFailure != nil {
				t.Fatal(headFailure)
			}
			if response != nil {
				_ = response.Body.Close()
			}
			e = owner.Run(ctx, func(scope *cooperative.Scope) error {
				if kind == "keepalive-disabled" {
					factory := NewPooledFactory(owner, scope, nil, pool)
					if e := factory.NativeKeepAliveDisable()(); e != nil {
						return e
					}
				}
				for range 2 {
					driver, e := pool.Driver(owner, scope, nil)
					if e != nil {
						return e
					}
					request, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/v1/messages", strings.NewReader(`{"owned":"model"}`))
					modelExchange := *exchange
					modelExchange.NativeTLSOptions = kind == "tls"
					result, e := driver.OpenFetch(request, &modelExchange)
					if e != nil {
						return e
					}
					body, e := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
						return scope.AwaitBytes(cooperative.StreamNext, work)
					})
					if e != nil {
						return e
					}
					_, e = body.ReadFetchBody(result.Response.Body)
					_ = result.Response.Body.Close()
					if e != nil {
						return e
					}
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			actual := append([]string{}, ports...)
			mu.Unlock()
			if len(actual) != 3 {
				t.Fatal("Go actual wire count", actual)
			}
			pattern := func(values []string) []bool { return []bool{values[0] == values[1], values[1] == values[2]} }
			if !reflect.DeepEqual(pattern(actual), pattern(expected)) {
				t.Fatalf("actual connection reuse Go%v (%v) Source%v (%v)", pattern(actual), actual, pattern(expected), expected)
			}
		})
	}
}
