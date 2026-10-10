//go:build darwin

package cooperative_test

import (
	"bufio"
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

// These profiles prove real resource/checkpoint ownership. They do not substitute
// for actual SDK Headers/status projection or the frozen Source phase profiles.
func TestReservedNativeHeadersOutliveDueClockAndBodyWait(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	parsed, headersWritten, bodyRead := make(chan struct{}), make(chan struct{}), make(chan struct{})
	joined := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			joined <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			joined <- err
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "{}" {
			joined <- fmt.Errorf("request bytes: %q %v", body, err)
			return
		}
		close(parsed)
		if _, err = io.WriteString(peer, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n"); err != nil {
			joined <- err
			return
		}
		close(headersWritten)
		<-bodyRead
		if _, err = io.WriteString(peer, "same"); err != nil {
			joined <- err
			return
		}
		remaining, err := io.ReadAll(peer)
		if err == nil && len(remaining) != 0 {
			err = errors.New("duplicate physical request")
		}
		joined <- err
	}()
	owner := cooperative.New()
	defer owner.Close()
	fired := 0
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		authority := listener.Addr().String()
		wire := []byte("POST /anthropic/v1/messages HTTP/1.1\r\nHost: " + authority + "\r\nContent-Length: 2\r\n\r\n{}")
		operation, err := provider.NewNativeHTTP1Reservation(context.Background(), authority, wire, 1024)
		if err != nil {
			return err
		}
		defer operation.Close()
		ticket, err := scope.ArmTimer(10*time.Millisecond, func() { fired++; _ = operation.Close() })
		if err != nil {
			return err
		}
		defer scope.ClearTimer(ticket)
		if err = scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
			<-parsed
			<-headersWritten
			<-cooperative.TimerDueForTesting(ticket)
			return nil
		}); err != nil {
			return err
		}
		header, err := scope.AwaitNative(operation)
		if err != nil {
			return err
		}
		if fired != 0 || string(header) != "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n" {
			return errors.New("due clock defeated fresh physical headers")
		}
		response, err := operation.AdoptResponse(nil)
		if err != nil {
			return err
		}
		if err = scope.ClearTimer(ticket); err != nil {
			return err
		}
		if cooperative.TimerActiveForTesting(ticket) {
			return errors.New("fetch finally did not invalidate ticket before body")
		}
		body, err := scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { close(bodyRead); return io.ReadAll(response.Body) })
		if err != nil || string(body) != "same" {
			return fmt.Errorf("same body after clear: %q %v", body, err)
		}
		return response.Body.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-joined; err != nil {
		t.Fatal(err)
	}
	if fired != 0 {
		t.Fatal("cleared clock fired during body")
	}
}

func TestReservedNativePartialAndAbsentHeadersRunOwnerPolicy(t *testing.T) {
	for _, prefix := range []string{"", "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n"} {
		t.Run(fmt.Sprintf("prefix-%d", len(prefix)), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			written, joined := make(chan struct{}), make(chan error, 1)
			go func() {
				peer, err := listener.Accept()
				if err != nil {
					joined <- err
					return
				}
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
				request, err := http.ReadRequest(bufio.NewReader(peer))
				if err != nil {
					joined <- err
					return
				}
				if _, err = io.ReadAll(request.Body); err != nil {
					joined <- err
					return
				}
				if _, err = io.WriteString(peer, prefix); err != nil {
					joined <- err
					return
				}
				close(written)
				_, err = io.Copy(io.Discard, peer)
				joined <- err
			}()
			owner := cooperative.New()
			defer owner.Close()
			fired := 0
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				authority := listener.Addr().String()
				wire := []byte("POST / HTTP/1.1\r\nHost: " + authority + "\r\nContent-Length: 2\r\n\r\n{}")
				operation, err := provider.NewNativeHTTP1Reservation(context.Background(), authority, wire, 1024)
				if err != nil {
					return err
				}
				defer operation.Close()
				ticket, err := scope.ArmTimer(10*time.Millisecond, func() { fired++; _ = operation.Close() })
				if err != nil {
					return err
				}
				defer scope.ClearTimer(ticket)
				if err = scope.Inline(cooperative.Read, func(*cooperative.Scope) error { <-written; <-cooperative.TimerDueForTesting(ticket); return nil }); err != nil {
					return err
				}
				_, err = scope.AwaitNative(operation)
				if !errors.Is(err, net.ErrClosed) {
					return fmt.Errorf("original timeout resource close: %v", err)
				}
				if fired != 1 {
					return fmt.Errorf("owner timeout count %d", fired)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = <-joined; err != nil {
				t.Fatal(err)
			}
		})
	}
}
