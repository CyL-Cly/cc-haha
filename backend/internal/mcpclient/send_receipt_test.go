package mcpclient

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sendReceiptTransport struct {
	started, release chan struct{}
	once             sync.Once
	writes           atomic.Int32
	failure          error
	receive          func(rpcMessage)
}

func (t *sendReceiptTransport) start(_ context.Context, receive func(rpcMessage), _ func(error), _ func()) error {
	t.receive = receive
	return nil
}
func (t *sendReceiptTransport) send(ctx context.Context, message rpcMessage) error {
	t.once.Do(func() { close(t.started) })
	select {
	case <-t.release:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if t.failure != nil {
		return t.failure
	}
	t.writes.Add(1)
	t.receive(rpcMessage{JSONRPC: "2.0", ID: message.ID, Result: []byte(`{}`)})
	return nil
}
func (*sendReceiptTransport) setProtocolVersion(string) {}
func (*sendReceiptTransport) close() error              { return nil }

func TestRequestActualSendReceiptExactOnceSuccessFailureAndQueuedCancellation(t *testing.T) {
	for _, profile := range []string{"success", "send-error", "queued-close", "already-closed", "already-cancelled"} {
		t.Run(profile, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			life, stop := context.WithCancel(context.Background())
			defer stop()
			transport := &sendReceiptTransport{started: make(chan struct{}), release: make(chan struct{})}
			client := &Client{ctx: life, cancel: stop, transport: transport, pending: map[string]pending{}, inbound: map[string]*inbound{}, options: Options{RequestTimeout: time.Second}}
			_ = transport.start(life, client.receive, nil, nil)
			if profile == "send-error" {
				transport.failure = io.ErrClosedPipe
			}
			if profile == "already-closed" {
				_ = client.Close()
			}
			requestCtx, requestCancel := context.WithCancel(ctx)
			defer requestCancel()
			if profile == "already-cancelled" {
				requestCancel()
			}
			ack := make(chan error, 2)
			var count atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := client.Request(requestCtx, "owned-cleanup", protocol.NewJSONObject(), RequestOptions{OnSent: func(err error) { count.Add(1); ack <- err }})
				done <- err
			}()
			if profile != "already-closed" && profile != "already-cancelled" {
				select {
				case <-transport.started:
				case <-ctx.Done():
					t.Fatal("send worker not started")
				}
				select {
				case err := <-ack:
					t.Fatal("queued packet falsely acknowledged as sent", err)
				default:
				}
				if profile == "queued-close" {
					_ = client.Close()
				} else {
					close(transport.release)
				}
			}
			var sentErr, requestErr error
			select {
			case sentErr = <-ack:
			case <-ctx.Done():
				t.Fatal("send failure/close omitted receipt")
			}
			select {
			case requestErr = <-done:
			case <-ctx.Done():
				t.Fatal("actual request not settled")
			}
			if profile == "success" {
				if sentErr != nil || requestErr != nil || transport.writes.Load() != 1 {
					t.Fatal(sentErr, requestErr, transport.writes.Load())
				}
			} else {
				if sentErr == nil || requestErr == nil || transport.writes.Load() != 0 {
					t.Fatal("unsent request reported wire success", sentErr, requestErr, transport.writes.Load())
				}
			}
			if count.Load() != 1 {
				t.Fatal("send receipt repeated", count.Load())
			}
			_ = client.Close()
		})
	}
}

type partialReceiptWriter struct{}

func (partialReceiptWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
func (partialReceiptWriter) Close() error                   { return nil }
func TestStdioPartialMessageCannotProduceSuccessfulSendReceipt(t *testing.T) {
	transport := &stdioTransport{stdin: partialReceiptWriter{}}
	if err := transport.send(context.Background(), rpcMessage{JSONRPC: "2.0", Method: "close_tab", Params: protocol.NewJSONObject(), hasMethod: true}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("partial physical write was treated as a complete message", err)
	}
}
