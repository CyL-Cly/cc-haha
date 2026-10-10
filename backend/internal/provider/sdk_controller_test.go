package provider

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"strings"
	"testing"
)

type controllerCanceledBody struct {
	io.Reader
	context context.Context
}

func (b *controllerCanceledBody) Read(dst []byte) (int, error) {
	n, e := b.Reader.Read(dst)
	if n > 0 {
		return n, e
	}
	<-b.context.Done()
	return 0, context.Cause(b.context)
}
func (b *controllerCanceledBody) Close() error { return nil }

// The raw resource context can abort while the original caller remains live.
// Pinned SDK only catches that actual AbortError; other errors must propagate.
func TestOriginalIndependentSDKResourceAbortBecomesIteratorEOF(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	physical, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &controllerCanceledBody{Reader: strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"), context: physical}
	original := context.Background()
	raw := &RawMessageStream{ctx: original, cancel: cancel, body: body, decoder: protocol.NewSDKSSEDecoder(nil)}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stream, e := NewOwnedMessageStream(scope, owner, raw)
		if e != nil {
			return e
		}
		first, e := stream.Iterator.Next(scope)
		if e != nil {
			return e
		}
		result, e := first.Await(scope)
		if e != nil || result.Done {
			return errors.New("original first event missing")
		}
		second, e := stream.Iterator.Next(scope)
		if e != nil {
			return e
		}
		// This calls the actual owned SDK controller after the adapter is installed.
		// Baseline resource-only cancellation is its existing missing semantic seam.
		if e = stream.AbortSDK(scope); e != nil {
			return e
		}
		result, e = second.Await(scope)
		if e != nil {
			return e
		}
		if !result.Done {
			return errors.New("SDK AbortError did not finish iterator")
		}
		if original.Err() != nil {
			return errors.New("SDK controller canceled original caller")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("actual Source SDK resource abort should finish lower iterator without caller abort: %v", err)
	}
}
