package providernative

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func bodyConfig(owner *cooperative.Owner, scope *cooperative.Scope, origin string, observer func(provider.OwnerNativeEvent)) (provider.AnthropicConfig, error) {
	config := nativeConfig(origin)
	driver, err := New(owner, scope, observer)
	if err != nil {
		return config, err
	}
	config.OwnerNativeIO = driver
	config.OwnerBodyIO, err = provider.NewOwnerBodyIO(driver, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		if kind != provider.OwnerBodyRead && kind != provider.OwnerErrorBody {
			return nil, cooperative.ErrRestricted
		}
		return scope.AwaitBytes(cooperative.StreamNext, work)
	})
	return config, err
}

func TestNativeErrorBodyWaitKeepsOwnerAndExactHTTPError(t *testing.T) {
	release := make(chan struct{})
	header := make(chan struct{})
	released := false
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		value := `{"type":"error","error":{"type":"api_error","message":"private native original"}}`
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 500 Error\r\nContent-Length: %d\r\nContent-Type: application/json\r\nRequest-Id: error-original\r\n\r\n", len(value))
		<-release
		_, _ = io.WriteString(conn, value)
	})
	defer func() {
		if !released {
			close(release)
		}
	}()
	owner := cooperative.New()
	defer owner.Close()
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			config, err := bodyConfig(owner, scope, origin, func(event provider.OwnerNativeEvent) {
				if event.Kind == "timer-clear" {
					close(header)
				}
			})
			if err != nil {
				return err
			}
			_, err = provider.NewAnthropic(config).OpenMessagesStream(context.Background(), originalPrepared())
			var api *provider.SDKStreamError
			if !errors.As(err, &api) || api.Status == nil || *api.Status != 500 || api.RequestID == nil || *api.RequestID != "error-original" || !strings.Contains(api.Message, "private native original") {
				return fmt.Errorf("original HTTP error ownership: %T %v", err, err)
			}
			return nil
		})
	}()
	<-header
	receipt, err := owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	moving := false
	select {
	case <-receipt.Done():
		moving = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	joinedPeer(t, peer)
	if !moving {
		t.Fatal("native HTTP error body blocked original owner until external body release")
	}
}

func TestNativeBodyAbortStopAndAdministrativeClose(t *testing.T) {
	for _, mode := range []string{"model-cancel", "consumer-stop", "owner-close"} {
		t.Run(mode, func(t *testing.T) {
			prefix := make(chan struct{})
			returned := make(chan error, 1)
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
				value := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
				_, _ = fmt.Fprintf(conn, "%x\r\n%s\r\n", len(value), value)
			})
			owner := cooperative.New()
			defer owner.Close()
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stream *provider.RawMessageStream
			go func() {
				returned <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					config, err := bodyConfig(owner, scope, origin, nil)
					if err != nil {
						return err
					}
					stream, err = provider.NewAnthropic(config).OpenMessagesStream(caller, originalPrepared())
					if err != nil {
						return err
					}
					defer stream.Close()
					raw, err := stream.Next()
					if err != nil || string(raw) != `{"type":"message_start"}` {
						return fmt.Errorf("first demand changed: %s %v", raw, err)
					}
					close(prefix)
					_, err = stream.Next()
					if mode == "owner-close" {
						if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrClosed) || stream.AbortCause() != nil {
							return fmt.Errorf("administrative body close changed: %v cause=%v", err, stream.AbortCause())
						}
						return nil
					}
					if mode == "model-cancel" && err != io.EOF {
						return fmt.Errorf("quiet model abort changed: %v", err)
					}
					if mode == "consumer-stop" && err != io.EOF && !errors.Is(err, net.ErrClosed) {
						return fmt.Errorf("concurrent resource close ownership: %v", err)
					}
					if mode == "model-cancel" && stream.AbortCause() != context.Canceled {
						return fmt.Errorf("caller abort reason: %v", stream.AbortCause())
					}
					if mode == "consumer-stop" && stream.AbortCause() != nil {
						return fmt.Errorf("consumer stop became model abort: %v", stream.AbortCause())
					}
					return nil
				})
			}()
			<-prefix
			if mode == "owner-close" {
				owner.Close()
			} else {
				receipt, err := owner.Submit(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error {
					if mode == "model-cancel" {
						cancel()
						return nil
					}
					return stream.Close()
				})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-receipt.Done():
				case <-time.After(time.Second):
					t.Fatal("cancel/close callback could not run during native body wait")
				}
			}
			select {
			case err := <-returned:
				if err != nil && !(mode == "owner-close" && errors.Is(err, cooperative.ErrClosed)) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("native body worker did not join")
			}
			joinedPeer(t, peer)
			if mode != "owner-close" {
				if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
					t.Fatal("model resource cancellation closed owner lifetime", err)
				}
			}
		})
	}
}

func TestNativeBodyEOFAndStaleDriverDoNotCrossASK(t *testing.T) {
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 0\r\n\r\n")
	})
	owner := cooperative.New()
	defer owner.Close()
	var bodyDriver *provider.OwnerBodyIO
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		config, err := bodyConfig(owner, scope, origin, nil)
		if err != nil {
			return err
		}
		bodyDriver = config.OwnerBodyIO
		stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), originalPrepared())
		if err != nil {
			return err
		}
		defer stream.Close()
		if _, err = stream.Next(); err != io.EOF || stream.AbortCause() != nil {
			return fmt.Errorf("plain empty native EOF changed: %v %v", err, stream.AbortCause())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	joinedPeer(t, peer)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		config, err := bodyConfig(owner, scope, "http://127.0.0.1:1", nil)
		if err != nil {
			return err
		}
		config.OwnerBodyIO = bodyDriver
		_, err = provider.NewAnthropic(config).OpenMessagesStream(context.Background(), originalPrepared())
		if !errors.Is(err, provider.ErrHTTPExchangeProfile) {
			return fmt.Errorf("previous ASK body/native identity silently reused: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
