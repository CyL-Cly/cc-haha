package providernative

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// The gate belongs to the external physical Read invocation. It establishes
// Close-before-beginRead, without guessing worker scheduling or SDK phases.
func TestNativeConsumerCloseBeforePhysicalReadAdmission(t *testing.T) {
	for _, mode := range []string{"consumer-close", "caller-abort", "owner-close"} {
		t.Run(mode, func(t *testing.T) {
			admitted, release := make(chan struct{}), make(chan struct{})
			physicalContext := make(chan context.Context, 1)
			var releaseOnce sync.Once
			releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseWorker()
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				prefix := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
				_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", len(prefix), prefix)
			})
			owner := cooperative.New()
			defer func() { releaseWorker(); owner.Close() }()
			caller, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			original := errors.New("original caller reason")
			var stream *provider.RawMessageStream
			done := make(chan error, 1)
			go func() {
				done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					config := nativeConfig(origin)
					native, err := New(owner, scope, nil)
					if err != nil {
						return err
					}
					config.OwnerNativeIO = native
					reads := 0
					config.OwnerBodyIO, err = provider.NewOwnerBodyIO(native, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
						if kind != provider.OwnerBodyRead {
							return nil, cooperative.ErrRestricted
						}
						reads++
						if reads == 2 {
							physical := work
							work = func(ctx context.Context) ([]byte, error) {
								physicalContext <- ctx
								close(admitted)
								<-release
								return physical(ctx)
							}
						}
						return scope.AwaitBytes(cooperative.StreamNext, work)
					})
					if err != nil {
						return err
					}
					stream, err = provider.NewAnthropic(config).OpenMessagesStream(caller, originalPrepared())
					if err != nil {
						return err
					}
					defer stream.Close()
					first, err := stream.Next()
					if err != nil || string(first) != `{"type":"message_start"}` {
						return fmt.Errorf("first event: %s %v", first, err)
					}
					_, err = stream.Next()
					switch mode {
					case "consumer-close":
						if !errors.Is(err, net.ErrClosed) || provider.IsOwnerWaitFailure(err) || stream.AbortCause() != nil || caller.Err() != nil {
							return fmt.Errorf("consumer close fabricated abort: read=%v abort=%v caller=%v", err, stream.AbortCause(), caller.Err())
						}
					case "caller-abort":
						if err != io.EOF || !errors.Is(stream.AbortCause(), context.Canceled) || context.Cause(caller) != original {
							return fmt.Errorf("caller abort identity: %v %v %v", err, stream.AbortCause(), context.Cause(caller))
						}
					case "owner-close":
						if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrClosed) || stream.AbortCause() != nil || caller.Err() != nil {
							return fmt.Errorf("administrative identity: %v %v", err, stream.AbortCause())
						}
					}
					if reads != 2 {
						return fmt.Errorf("read demands %d", reads)
					}
					return nil
				})
			}()
			select {
			case <-admitted:
			case err := <-done:
				t.Fatalf("read not admitted: %v", err)
			case <-time.After(3 * time.Second):
				releaseWorker()
				owner.Close()
				<-done
				t.Fatal("physical admission not reached")
			}
			if mode == "owner-close" {
				closed := make(chan struct{})
				go func() { owner.Close(); close(closed) }()
				physical := <-physicalContext
				<-physical.Done()
				releaseWorker()
				<-closed
			} else {
				receipt, err := owner.Submit(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error {
					if mode == "caller-abort" {
						cancel(original)
						return nil
					}
					return stream.Close()
				})
				if err != nil {
					releaseWorker()
					<-done
					t.Fatal(err)
				}
				<-receipt.Done()
				if err = receipt.Wait(); err != nil {
					releaseWorker()
					<-done
					t.Fatal(err)
				}
			}
			releaseWorker()
			if err := <-done; err != nil {
				t.Error(err)
			}
			joinedPeer(t, peer)
			if mode != "owner-close" {
				if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
					t.Error("resource closure canceled Owner", err)
				}
			}
		})
	}
}

func TestNativeOwnedClosedLeaseIsPhysicalFaultNotAbort(t *testing.T) {
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
	})
	owner := cooperative.New()
	defer owner.Close()
	caller := context.Background()
	err := owner.Run(caller, func(scope *cooperative.Scope) error {
		config, err := bodyConfig(owner, scope, origin, nil)
		if err != nil {
			return err
		}
		raw, err := provider.NewAnthropic(config).OpenMessagesStream(caller, originalPrepared())
		if err != nil {
			return err
		}
		defer raw.Close()
		owned, err := provider.NewOwnedMessageStream(scope, owner, raw)
		if err != nil {
			return err
		}
		if err = owned.Close(); err != nil {
			return err
		}
		next, err := owned.Iterator.Next(scope)
		if err != nil {
			return err
		}
		result, err := next.Await(scope)
		if !errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || provider.IsOwnerWaitFailure(err) || raw.AbortCause() != nil || caller.Err() != nil {
			return fmt.Errorf("closed owned lease identity: done=%v failure=%v callerAbort=%v", result.Done, err, raw.AbortCause())
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	joinedPeer(t, peer)
}
