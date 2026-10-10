package providernative

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNativeHeadersBodyReadKeepsOwnerCallbacksMoving(t *testing.T) {
	release := make(chan struct{})
	header := make(chan struct{})
	released := false
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
		<-release
		value := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		_, _ = io.WriteString(conn, fmt.Sprintf("%x\r\n%s\r\n0\r\n\r\n", len(value), value))
	})
	defer func() {
		if !released {
			close(release)
		}
	}()
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			config := nativeConfig(origin)
			config.Timeout = time.Second
			driver, err := New(owner, scope, func(e provider.OwnerNativeEvent) {
				if e.Kind == "fetch-fulfilled" {
					close(header)
				}
			})
			if err != nil {
				return err
			}
			config.OwnerNativeIO = driver
			config.OwnerBodyIO, err = provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if err != nil {
				return err
			}
			stream, err := provider.NewAnthropic(config).OpenMessagesStream(ctx, originalPrepared())
			if err != nil {
				return err
			}
			defer stream.Close()
			_, err = stream.Next()
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return nil
		})
	}()
	<-header
	receipt, err := owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	starved := false
	select {
	case <-receipt.Done():
	case <-time.After(200 * time.Millisecond):
		starved = true
	}
	close(release)
	released = true
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
	joinedPeer(t, peer)
	if starved {
		t.Fatal("native body Read blocked original owner callback before tail release")
	}
}
