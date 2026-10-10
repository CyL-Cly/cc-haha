package providernative

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// This uses the real native header driver and a demanded physical HTTP-error
// body. Administrative owner failure is not a Source AbortSignal observation.
func TestNativeErrorBodyPreservesAdministrativeCompletion(t *testing.T) {
	for _, mode := range []string{"owner-close", "scope-cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				_, _ = io.WriteString(conn, "HTTP/1.1 500 Error\r\nContent-Length: 200\r\nContent-Type: application/json\r\nRequest-Id: admin-original\r\n\r\n")
			})
			owner := cooperative.New()
			defer owner.Close()
			caller := context.Background()
			runctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			original := errors.New("original ASK scope cancelled independently of model")
			if mode == "owner-close" {
				original = cooperative.ErrClosed
			}
			result := make(chan error, 1)
			runresult := make(chan error, 1)
			go func() {
				runresult <- owner.Run(runctx, func(scope *cooperative.Scope) error {
					config := nativeConfig(origin)
					native, err := New(owner, scope, nil)
					if err != nil {
						result <- err
						return err
					}
					config.OwnerNativeIO = native
					config.OwnerBodyIO, err = provider.NewOwnerBodyIO(native, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
						if kind != provider.OwnerErrorBody {
							return nil, fmt.Errorf("unexpected wait %v", kind)
						}
						close(entered)
						return scope.AwaitBytes(cooperative.StreamNext, work)
					})
					if err != nil {
						result <- err
						return err
					}
					stream, err := provider.NewAnthropic(config).OpenMessagesStream(caller, originalPrepared())
					if stream != nil {
						stream.Close()
						err = errors.New("HTTP error acquired stream")
					}
					result <- err
					return nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("native body not demanded")
			}
			if mode == "owner-close" {
				owner.Close()
			} else {
				cancel(original)
			}
			failure := <-result
			<-runresult
			joinedPeer(t, peer)
			t.Logf("NATIVE_ADMIN mode=%s type=%T error=%v typed=%t cause=%t", mode, failure, failure, provider.IsOwnerWaitFailure(failure), errors.Is(failure, original))
			var sdk *provider.SDKStreamError
			if !provider.IsOwnerWaitFailure(failure) || !errors.Is(failure, original) || errors.As(failure, &sdk) || caller.Err() != nil {
				t.Fatalf("native owner body failure lost typed identity: %T %v", failure, failure)
			}
		})
	}
}
